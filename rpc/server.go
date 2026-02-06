package rpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	sync "sync"
	"sync/atomic"

	"github.com/cloudwego/netpoll"
	"github.com/fixkme/gokit/errs"
	"github.com/fixkme/gokit/mlog"

	"google.golang.org/protobuf/proto"
)

type Server struct {
	opt      *ServerOptions
	listener netpoll.Listener
	evloop   netpoll.EventLoop
	services map[string]*serviceInfo // service name -> service info
	conns    map[netpoll.Connection]*SvrMuxConn
	ctx      context.Context
	closed   atomic.Bool // server closed
}

type ServerOptions struct {
	ListenAddr     string //在某些情况不能和rpcAddr一样，比如域名、k8s service，所以单独控制
	PollerNum      int
	BufferSize     int // default size of a new connection's read LinkBuffer
	PollOpts       []netpoll.Option
	HandlerFunc    RpcHandler
	MsgUnmarshaler Unmarshaler
	MsgMarshaler   Marshaler
}

type RpcHandler func(rc *RpcContext)

func NewServer(opt *ServerOptions, ctx context.Context) (*Server, error) {
	s := &Server{
		services: make(map[string]*serviceInfo),
		conns:    make(map[netpoll.Connection]*SvrMuxConn),
		opt:      opt,
		ctx:      ctx,
	}
	pollConfig := netpoll.Config{
		PollerNum:  s.opt.PollerNum,
		BufferSize: s.opt.BufferSize,
	}
	err := netpoll.Configure(pollConfig)
	if err != nil {
		return nil, err
	}
	s.listener, err = netpoll.CreateListener("tcp", opt.ListenAddr)
	if err != nil {
		return nil, err
	}

	if s.opt.PollOpts == nil {
		s.opt.PollOpts = []netpoll.Option{}
	}
	s.opt.PollOpts = append(s.opt.PollOpts, netpoll.WithOnPrepare(s.prepare))
	s.evloop, err = netpoll.NewEventLoop(s.onMsg, s.opt.PollOpts...)
	if err != nil {
		return nil, err
	}
	s.opt.PollOpts = nil // clean after use

	opt.setDefault()

	return s, nil
}

func (opt *ServerOptions) setDefault() {
	if opt.MsgUnmarshaler == nil {
		opt.MsgUnmarshaler = proto.UnmarshalOptions{}
	}
	if opt.MsgMarshaler == nil {
		opt.MsgMarshaler = proto.MarshalOptions{}
	}
	if opt.HandlerFunc == nil {
		opt.HandlerFunc = func(rc *RpcContext) {
			argMsg, handler := rc.ReqMsg, rc.Handler
			rc.Reply, rc.ReplyErr = handler(context.Background(), argMsg)
			rc.SerializeResponse()
			// 或者投递给另一个协程执行 ...
		}
	}
}

func (s *Server) Run() error {
	if err := s.evloop.Serve(s.listener); err != nil {
		mlog.Errorf("rpc server Run err:%s", err)
		return err
	}
	mlog.Info("rpc server Run exit")
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if old := s.closed.Swap(true); old {
		return nil
	}
	mlog.Debug("rpc server stop")
	s.listener.Close()
	for c := range s.conns {
		if err := c.Close(); err != nil {
			mlog.Errorf("rpc conn close error %v", err)
		}
	}
	if err := s.evloop.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Server) RegisterService(sd *ServiceDesc, ss any) {
	if ss != nil {
		ht := reflect.TypeOf(sd.HandlerType).Elem()
		st := reflect.TypeOf(ss)
		if !st.Implements(ht) {
			err := fmt.Errorf("rpc: Server.RegisterService found the handler of type %v that does not satisfy %v", st, ht)
			panic(err)
		}
	}
	s.register(sd, ss)
}

func (s *Server) register(sd *ServiceDesc, ss any) {
	if _, ok := s.services[sd.ServiceName]; ok {
		err := fmt.Errorf("rpc: Server.RegisterService found duplicate service registration for %q", sd.ServiceName)
		panic(err)
	}
	info := &serviceInfo{
		serviceImpl: ss,
		methods:     make(map[string]*MethodDesc),
	}
	for i := range sd.Methods {
		d := &sd.Methods[i]
		info.methods[d.MethodName] = d
	}
	s.services[sd.ServiceName] = info
}

func (s *Server) handler(mc *SvrMuxConn, msg *RpcRequestMessage) {
	rc := rpcContextPool.Get()
	rc.Conn = mc
	rc.Seq = msg.Seq
	rc.MethodName = msg.MethodName
	rc.ReqMd = msg.Md
	rc.marshaler = s.opt.MsgMarshaler

	var md *MethodDesc
	serviceInfo, ok := s.services[msg.ServiceName]
	if ok {
		md, ok = serviceInfo.methods[msg.MethodName]
		if !ok {
			rc.ReplyErr = fmt.Errorf("not found method:%s", msg.MethodName)
			rc.SerializeResponse()
			return
		}
	} else {
		rc.ReplyErr = fmt.Errorf("not found service:%s", msg.ServiceName)
		rc.SerializeResponse()
		return
	}

	rc.ReqMsg, rc.Handler = md.Handler(serviceInfo.serviceImpl)
	if err := s.opt.MsgUnmarshaler.Unmarshal(msg.Payload, rc.ReqMsg); err != nil {
		rc.ReplyErr = err
		rc.SerializeResponse()
		return
	}

	s.opt.HandlerFunc(rc)
}

const msgLenSize = 4 //32bits uint32

type connectionContextKey string

const connectionContextName = connectionContextKey("ConnectionContext")

func (s *Server) prepare(conn netpoll.Connection) context.Context {
	mc := newSvrMuxConn(conn)
	s.conns[conn] = mc
	conn.AddCloseCallback(func(c netpoll.Connection) error {
		mlog.Debugf("server conn closed %v", c == conn)
		delete(s.conns, c)
		return nil
	})
	ctx := context.WithValue(s.ctx, connectionContextName, mc)
	return ctx
}

func (s *Server) onMsg(ctx context.Context, c netpoll.Connection) (err error) {
	defer func() {
		if err != nil {
			mlog.Errorf("rpc server read cli msg err:%v\n", err)
			if errors.Is(err, netpoll.ErrReadTimeout) {
				c.Close()
			}
		}
	}()
	mc := ctx.Value(connectionContextName).(*SvrMuxConn)
	reader := c.Reader()

	lenBuf, _err := reader.Next(msgLenSize)
	if err = _err; err != nil {
		mlog.Debugf("server onMsg read msgLenBuf err:%v", err)
		return
	}
	dataLen := int(byteOrder.Uint32(lenBuf))
	packetBuf, _err := reader.Next(dataLen)
	if err = _err; err != nil {
		mlog.Debugf("server onMsg read msg data err:%v", err)
		return
	}
	// 反序列化
	msg := &RpcRequestMessage{}
	if err = rpcMsgUnmarshaler.Unmarshal(packetBuf, msg); err != nil {
		mlog.Errorf("proto.Unmarshal RpcRequestMessage err:%v\n", err)
		return
	}

	s.handler(mc, msg)
	return
}

type RpcContext struct {
	Conn       *SvrMuxConn
	Seq        uint32
	MethodName string
	ReqMd      *Meta
	ReqMsg     proto.Message
	Handler    MsgHandler

	Reply     proto.Message
	ReplyErr  error
	ReplyMd   *Meta
	marshaler Marshaler
}

func (rc *RpcContext) SerializeResponse() {
	defer rpcContextPool.Put(rc)

	if !rc.Conn.c.IsActive() {
		mlog.Warn("server conn is not active when serializing response")
		return
	}

	rsp := new(RpcResponseMessage)
	rsp.Seq = rc.Seq
	rsp.Md = rc.ReplyMd
	if rerr := rc.ReplyErr; rerr == nil {
		rspData, err := rc.marshaler.Marshal(rc.Reply)
		if err == nil {
			rsp.Payload = rspData
		} else {
			rsp.Ecode = errs.ErrCode_Marshal
			rsp.Error = err.Error()
		}
	} else {
		if cerr, ok := rerr.(errs.CodeError); ok {
			rsp.Ecode = cerr.Code()
			rsp.Error = cerr.Error()
		} else {
			rsp.Ecode = errs.ErrCode_Unknown
			rsp.Error = rerr.Error()
		}
	}

	// 反序列化rpc rsp
	sz := rpcMsgMarshaler.Size(rsp)
	buffer := netpoll.NewLinkBuffer(msgLenSize + sz)
	// 获取整个写入空间(长度头+消息体)
	buf, err := buffer.Malloc(msgLenSize + sz)
	if err != nil {
		mlog.Errorf("rpc server serializeResponse buffer.Malloc err:%v\n", err)
		return
	}
	data, err := rpcMsgMarshaler.MarshalAppend(buf[msgLenSize:msgLenSize], rsp) // len=0,cap=len(buf)-msgLenSize
	if err != nil {
		mlog.Errorf("rpc server serializeResponse proto.MarshalAppend err:%v\n", err)
		return
	}
	byteOrder.PutUint32(buf[:msgLenSize], uint32(len(data)))
	if len(data) == len(buf[msgLenSize:]) {
		mlog.Debugf("rpc server serializeResponse sync send response %v", rc.Conn.c.IsActive())
		if err = rc.Conn.FlushBuffer(buffer); err != nil {
			mlog.Errorf("rpc server serializeResponse FlushBuffer err:%v\n", err)
			return
		}
	} else {
		mlog.Errorf("rpc server serializeResponse size wrong %d, %d\n", len(data), len(buf[msgLenSize:]))
		return
	}
}

func newSvrMuxConn(conn netpoll.Connection) *SvrMuxConn {
	mc := &SvrMuxConn{c: conn}
	return mc
}

type SvrMuxConn struct {
	c   netpoll.Connection
	mtx sync.Mutex
}

func (mc *SvrMuxConn) FlushBuffer(buffer *netpoll.LinkBuffer) (err error) {
	mc.mtx.Lock()
	defer mc.mtx.Unlock()
	if err = mc.c.Writer().Append(buffer); err != nil {
		return
	}
	if err = mc.c.Writer().Flush(); err != nil {
		return
	}
	return
}

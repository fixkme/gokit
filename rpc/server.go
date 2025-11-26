package rpc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"runtime/debug"
	sync "sync"

	"github.com/cloudwego/netpoll"
	"github.com/fixkme/gokit/errs"
	"github.com/fixkme/gokit/mlog"

	"google.golang.org/protobuf/proto"
)

type Server struct {
	opt      *ServerOpt
	listener netpoll.Listener
	evloop   netpoll.EventLoop
	conns    map[netpoll.Connection]*SvrMuxConn

	services   map[string]*serviceInfo // service name -> service info
	processors []*rpcProcessor
	quit       chan struct{}
	wg         sync.WaitGroup
	ctx        context.Context
}

type ServerOpt struct {
	ListenAddr        string
	PollerNum         int
	PollOpts          []netpoll.Option
	ProcessorSize     int64
	ProcessorTaskSize int64

	DispatcherFunc DispatchHash
	HandlerFunc    RpcHandler
}

type RpcHandler func(rc *RpcContext)
type DispatchHash func(netpoll.Connection, *RpcRequestMessage) int

func NewServer(opt *ServerOpt, ctx context.Context) (*Server, error) {
	s := &Server{
		services: make(map[string]*serviceInfo),
		conns:    make(map[netpoll.Connection]*SvrMuxConn),
		quit:     make(chan struct{}),
		opt:      opt,
		ctx:      ctx,
	}
	pollConfig := netpoll.Config{
		PollerNum: s.opt.PollerNum,
	}
	err := netpoll.Configure(pollConfig)
	if err != nil {
		return nil, err
	}
	s.listener, err = netpoll.CreateListener("tcp", s.opt.ListenAddr)
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

	if opt.HandlerFunc == nil {
		unmarshaler := proto.UnmarshalOptions{}
		marshaler := proto.MarshalOptions{}
		opt.HandlerFunc = func(rc *RpcContext) {
			argMsg, handler := rc.Method(rc.SrvImpl)
			if err := unmarshaler.Unmarshal(rc.Req.Payload, argMsg); err != nil {
				rc.ReplyErr = err
			} else {
				rc.Reply, rc.ReplyErr = handler(context.Background(), argMsg)
			}
			rc.SerializeResponse(&marshaler)
			// 或者投递给另一个actor协程 ...
		}
	}
	if opt.ProcessorSize > 0 {
		if opt.ProcessorTaskSize <= 0 {
			opt.ProcessorTaskSize = 1024
		}
		// 异步模式
		for i := 0; i < int(opt.ProcessorSize); i++ {
			s.processors = append(s.processors, &rpcProcessor{
				server: s,
				inChan: make(chan *rpcTask, opt.ProcessorTaskSize),
			})
		}
		if opt.DispatcherFunc == nil {
			// 默认随机分配
			opt.DispatcherFunc = func(c netpoll.Connection, msg *RpcRequestMessage) int {
				return rand.Int()
			}
		}
	}
	return s, nil
}

func (s *Server) Run() error {
	for i := 0; i < len(s.processors); i++ {
		s.wg.Add(1)
		go s.processors[i].run(s.quit)
	}
	if err := s.evloop.Serve(s.listener); err != nil {
		close(s.quit)
		return err
	}
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	mlog.Debug("rpc server stop")
	s.listener.Close()
	for c := range s.conns {
		if err := c.Close(); err != nil {
			mlog.Error("rpc conn close error %v", err)
		}
	}
	close(s.quit)
	s.wg.Wait()
	if err := s.evloop.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

type rpcTask struct {
	conn *SvrMuxConn
	msg  *RpcRequestMessage
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
	//mlog.Debug("%d start handler rpc msg:%s", g.GoroutineID(), msg.String())
	rc := new(RpcContext)
	rc.Conn = mc
	rc.Req = msg

	var md *MethodDesc
	serviceInfo, ok := s.services[msg.ServiceName]
	if ok {
		md, ok = serviceInfo.methods[msg.MethodName]
		if !ok {
			rc.ReplyErr = fmt.Errorf("not found method:%s", msg.MethodName)
			rc.SerializeResponse(nil)
			return
		}
	} else {
		rc.ReplyErr = fmt.Errorf("not found service:%s", msg.ServiceName)
		rc.SerializeResponse(nil)
		return
	}

	rc.SrvImpl = serviceInfo.serviceImpl
	rc.Method = md.Handler

	// handler msg，因为写回客户端操作可能跨协程，worker协程或者业务处理logic协程，所以传递s.serializeResponse
	s.opt.HandlerFunc(rc)
	//mlog.Debug("%d succeed handler rpc msg:%s", g.GoroutineID(), msg.String())
}

const msgLenSize = 4 //32bits uint32

type connectionContextKey string

const connectionContextName = connectionContextKey("ConnectionContext")

func (s *Server) prepare(conn netpoll.Connection) context.Context {
	mc := newSvrMuxConn(conn)
	s.conns[conn] = mc
	conn.AddCloseCallback(func(c netpoll.Connection) error {
		mlog.Debug("server conn closed %v", c == conn)
		delete(s.conns, c)
		return nil
	})
	ctx := context.WithValue(s.ctx, connectionContextName, mc)
	return ctx
}

func (s *Server) onMsg(ctx context.Context, c netpoll.Connection) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("onMsg panic:%v", r)
			mlog.Error("%v\n%s", err, debug.Stack())
		}
		if err != nil {
			mlog.Error("rpc server read cli msg err:%v\n", err)
		}
	}()
	mc := ctx.Value(connectionContextName).(*SvrMuxConn)
	reader := c.Reader()
	if reader.Len() == 0 {
		mlog.Debug("server onMsg cli EOF")
		// 对端发送 FIN，正常关闭
		c.Close()
		return
	}
	for reader.Len() > msgLenSize {
		lenBuf, _err := reader.Peek(msgLenSize)
		if err = _err; err != nil {
			if errors.Is(err, netpoll.ErrConnClosed) {
				mlog.Debug("server onMsg cli closed")
				err = nil
				return
			}
			return
		}
		dataLen := int(byteOrder.Uint32(lenBuf))
		totalLen := msgLenSize + dataLen
		if reader.Len() < totalLen {
			return
		}
		if err = reader.Skip(msgLenSize); err != nil {
			return
		}
		packetBuf, _err := reader.Next(dataLen)
		if err = _err; err != nil {
			return
		}
		// 反序列化
		msg := &RpcRequestMessage{}
		if err = rpcMsgUnmarshaler.Unmarshal(packetBuf, msg); err != nil {
			mlog.Error("proto.Unmarshal RpcRequestMessage err:%v\n", err)
			return
		}

		if pn := len(s.processors); pn > 0 {
			// 分发消息
			hashCode := s.opt.DispatcherFunc(c, msg)
			idx := hashCode % pn
			task := &rpcTask{conn: mc, msg: msg}
			select {
			case <-ctx.Done():
				err = ctx.Err()
				return
			case s.processors[idx].inChan <- task:
			default:
				mlog.Error("rpc processors[%d] inChan is full!!!", idx)
				s.processors[idx].inChan <- task
			}
		} else {
			// 直接处理
			s.handler(mc, msg)
		}
	}
	return
}

type rpcProcessor struct {
	server *Server
	inChan chan *rpcTask
}

func (p *rpcProcessor) run(done <-chan struct{}) {
	defer func() {
		p.server.wg.Done()
	}()
	for {
		select {
		case <-done:
			return
		case v, ok := <-p.inChan:
			if ok {
				p.server.handler(v.conn, v.msg)
			}
		}
	}
}

type RpcContext struct {
	Conn    *SvrMuxConn
	Req     *RpcRequestMessage
	SrvImpl any
	Method  MethodHandler

	Reply    proto.Message
	ReplyErr error
	ReplyMd  *Meta
}

func (rc *RpcContext) SerializeResponse(marshaler *proto.MarshalOptions) {
	if !rc.Conn.c.IsActive() {
		mlog.Warn("server conn is not active when serializing response")
		return
	}

	rsp := new(RpcResponseMessage)
	rsp.Seq = rc.Req.Seq
	rsp.Md = rc.ReplyMd
	if rerr := rc.ReplyErr; rerr == nil {
		rspData, err := marshaler.Marshal(rc.Reply)
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
		mlog.Error("rpc server serializeResponse buffer.Malloc err:%v\n", err)
		return
	}
	data, err := rpcMsgMarshaler.MarshalAppend(buf[msgLenSize:msgLenSize], rsp) // len=0,cap=len(buf)-msgLenSize
	if err != nil {
		mlog.Error("rpc server serializeResponse proto.MarshalAppend err:%v\n", err)
		return
	}
	byteOrder.PutUint32(buf[:msgLenSize], uint32(len(data)))
	if len(data) == len(buf[msgLenSize:]) {
		mlog.Debug("rpc server serializeResponse async send response %v", rc.Conn.c.IsActive())
		rc.Conn.mtx.Lock()
		defer rc.Conn.mtx.Unlock()
		if err = rc.Conn.c.Writer().Append(buffer); err != nil {
			mlog.Error("rpc server serializeResponse Writer.Append err:%v\n", err)
			return
		}
		if err = rc.Conn.c.Writer().Flush(); err != nil {
			mlog.Error("rpc server serializeResponse Writer.Flush err:%v\n", err)
			return
		}
	} else {
		mlog.Error("rpc server serializeResponse size wrong %d, %d\n", len(data), len(buf[msgLenSize:]))
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

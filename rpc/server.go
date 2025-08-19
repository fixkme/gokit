package rpc

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	sync "sync"

	"github.com/fixkme/gokit/mlog"

	"github.com/cloudwego/netpoll"
	"github.com/cloudwego/netpoll/mux"
	"github.com/fixkme/gokit/util/errs"
	g "github.com/fixkme/gokit/util/go"
	"google.golang.org/protobuf/proto"
)

type MethodHandler func(srv any) (proto.Message, Handler)

type MethodDesc struct {
	MethodName string
	Handler    MethodHandler
}

type ServiceDesc struct {
	ServiceName string
	HandlerType any
	Methods     []MethodDesc
}

type serviceInfo struct {
	serviceImpl any
	methods     map[string]*MethodDesc
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

type RpcHandler func(*RpcContext, ServerSerializer)

type ServerSerializer func(rc *RpcContext, sync bool)

type DispatchHash func(netpoll.Connection, *RpcRequestMessage) int

type Server struct {
	opt      *ServerOpt
	listener netpoll.Listener
	evloop   netpoll.EventLoop

	services   map[string]*serviceInfo // service name -> service info
	processors []*rpcProcessor
	quit       chan struct{}
	wg         sync.WaitGroup
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

func NewServer(opt *ServerOpt) (*Server, error) {
	s := &Server{
		services: make(map[string]*serviceInfo),
		quit:     make(chan struct{}),
		opt:      opt,
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
	s.opt.PollOpts = append(s.opt.PollOpts, netpoll.WithOnPrepare(prepare))
	s.evloop, err = netpoll.NewEventLoop(s.onMsg, s.opt.PollOpts...)
	if err != nil {
		return nil, err
	}

	if opt.HandlerFunc == nil {
		opt.HandlerFunc = func(rc *RpcContext, ser ServerSerializer) {
			argMsg, handler := rc.Method(rc.SrvImpl)
			if err := proto.Unmarshal(rc.Req.Payload, argMsg); err != nil {
				rc.ReplyErr = err
			} else {
				rc.Reply, rc.ReplyErr = handler(context.Background(), argMsg)
			}
			ser(rc, len(s.processors) == 0)
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
	if err := s.evloop.Shutdown(ctx); err != nil {
		close(s.quit)
		return err
	}
	close(s.quit)
	s.wg.Wait()
	return nil
}

type rpcTask struct {
	conn *SvrMuxConn
	msg  *RpcRequestMessage
}

type ServiceRegistrar interface {
	RegisterService(desc *ServiceDesc, impl any)
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
		err := fmt.Errorf("grpc: Server.RegisterService found duplicate service registration for %q", sd.ServiceName)
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
	mlog.Debug("%d start handler rpc msg:%s\n", g.GoroutineID(), msg.String())
	rc := new(RpcContext)
	rc.Conn = mc
	rc.Req = msg

	var md *MethodDesc
	serviceInfo, ok := s.services[msg.ServiceName]
	if ok {
		md, ok = serviceInfo.methods[msg.MethodName]
		if !ok {
			rc.ReplyErr = fmt.Errorf("not found method:%s", msg.MethodName)
			s.serializeResponse(rc, len(s.processors) == 0)
			return
		}
	} else {
		rc.ReplyErr = fmt.Errorf("not found service:%s", msg.ServiceName)
		s.serializeResponse(rc, len(s.processors) == 0)
		return
	}

	rc.SrvImpl = serviceInfo.serviceImpl
	rc.Method = md.Handler

	// handler msg
	s.opt.HandlerFunc(rc, s.serializeResponse)
	mlog.Debug("%d succeed handler rpc msg:%s\n", g.GoroutineID(), msg.String())
}

func (s *Server) serializeResponse(rc *RpcContext, sync bool) {
	rsp := new(RpcResponseMessage)
	rsp.Seq = rc.Req.Seq
	rsp.Md = rc.ReplyMd
	if rerr := rc.ReplyErr; rerr == nil {
		rspData, err := proto.Marshal(rc.Reply)
		if err == nil {
			rsp.Payload = rspData
		} else {
			rsp.Ecode = 1
			rsp.Error = err.Error()
		}
	} else {
		if cerr, ok := rerr.(errs.CodeError); ok {
			rsp.Ecode = cerr.Code()
			rsp.Error = cerr.Error()
		} else {
			rsp.Ecode = 1
			rsp.Error = rerr.Error()
		}
	}

	// 反序列化rpc rsp
	sz := defaultMarshaler.Size(rsp)
	buffer := netpoll.NewLinkBuffer(msgLenSize + sz)
	// 获取整个写入空间(长度头+消息体)
	buf, err := buffer.Malloc(msgLenSize + sz)
	if err != nil {
		mlog.Error("rpc server serializeResponse buffer.Malloc err:%v\n", err)
		return
	}
	data, err := defaultMarshaler.MarshalAppend(buf[msgLenSize:msgLenSize], rsp)
	if err != nil {
		mlog.Error("rpc server serializeResponse proto.MarshalAppend err:%v\n", err)
		return
	}
	byteOrder.PutUint32(buf[:msgLenSize], uint32(len(data)))
	if len(data) == len(buf[msgLenSize:]) {
		if !sync { //处于异步
			rc.Conn.Put(buffer)
		} else {
			_, err = rc.Conn.c.Write(buffer.Bytes())
			if err != nil {
				mlog.Error("rpc server serializeResponse Write err:%v\n", err)
				return
			}
		}
	} else {
		mlog.Error("rpc server serializeResponse size wrong %d, %d\n", len(data), len(buf[msgLenSize:]))
		return
	}
}

const msgLenSize = 4 //32bits uint32

func (s *Server) onMsg(ctx context.Context, c netpoll.Connection) (err error) {
	defer func() {
		if err != nil {
			mlog.Error("server read cli msg err:%v\n", err)
		}
	}()
	mc := ctx.Value(c).(*SvrMuxConn)
	reader := c.Reader()
	for {
		lenBuf, _err := reader.Peek(msgLenSize)
		if _err != nil {
			return _err
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
		if _err != nil {
			return _err
		}
		// 反序列化
		msg := &RpcRequestMessage{}
		if err = defaultUnmarshaler.Unmarshal(packetBuf, msg); err != nil {
			mlog.Error("proto.Unmarshal RpcRequestMessage err:%v\n", err)
			return err
		}

		if pn := len(s.processors); pn > 0 {
			// 分发消息
			hashCode := s.opt.DispatcherFunc(c, msg)
			idx := hashCode % pn
			s.processors[idx].inChan <- &rpcTask{conn: mc, msg: msg}
		} else {
			// 直接处理
			s.handler(mc, msg)
		}
	}
}

func (s *Server) makeRpcContext(mc *SvrMuxConn, msg *RpcRequestMessage) (ctx *RpcContext) {
	ctx = &RpcContext{
		Req:  msg,
		Conn: mc,
	}
	serviceInfo, ok := s.services[msg.ServiceName]
	if ok {
		ctx.SrvImpl = serviceInfo.serviceImpl
		md, ok := serviceInfo.methods[msg.MethodName]
		if ok {
			ctx.Method = md.Handler
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

func prepare(conn netpoll.Connection) context.Context {
	mc := newSvrMuxConn(conn)
	ctx := context.WithValue(context.Background(), conn, mc)
	return ctx
}

func newSvrMuxConn(conn netpoll.Connection) *SvrMuxConn {
	mc := &SvrMuxConn{}
	mc.c = conn
	mc.wqueue = mux.NewShardQueue(mux.ShardSize, conn)
	return mc
}

type SvrMuxConn struct {
	c      netpoll.Connection
	wqueue *mux.ShardQueue // use for async write
}

func (c *SvrMuxConn) Put(buffer *netpoll.LinkBuffer) {
	c.wqueue.Add(func() (buf netpoll.Writer, isNil bool) {
		return buffer, false
	})
}

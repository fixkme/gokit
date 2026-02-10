package rpc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
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
	conns    sync.Map                // map[netpoll.Connection]*SvrWriter
	ctx      context.Context
	wg       sync.WaitGroup
	closed   atomic.Bool // server closed
}

type ServerOptions struct {
	ListenAddr     string //在某些情况不能和rpcAddr一样，比如域名、k8s service，所以单独控制
	PollerNum      int
	BufferSize     int // size of a new connection's read LinkBuffer, netpoll default=8k
	PollOpts       []netpoll.Option
	HandlerFunc    RpcHandler
	MsgUnmarshaler Unmarshaler
	MsgMarshaler   Marshaler
	WriteChanSize  int
}

type RpcHandler func(rc *RpcContext)

func NewServer(opt *ServerOptions, ctx context.Context) (*Server, error) {
	s := &Server{
		services: make(map[string]*serviceInfo),
		conns:    sync.Map{},
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
	if opt.WriteChanSize <= 0 {
		opt.WriteChanSize = 1024
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
	if err := s.listener.Close(); err != nil {
		mlog.Errorf("rpc server close error %v", err)
	}

	writers := make([]*SvrWriter, 0)
	s.conns.Range(func(key, value any) bool {
		writers = append(writers, value.(*SvrWriter))
		return true
	})

	// 这里不用stop也行，因为上层rpc module会cancel Server.ctx
	for _, w := range writers {
		w.stop()
	}

	waitDone := make(chan struct{})
	go func() {
		s.wg.Wait() // wait svr writer flush over
		close(waitDone)
	}()
	select {
	case <-ctx.Done():
	case <-waitDone:
	}

	for _, w := range writers {
		if err := w.c.Close(); err != nil {
			mlog.Errorf("rpc svr conn close error %v", err)
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

func (s *Server) handler(w *SvrWriter, msg *RpcRequestMessage) {
	rc := rpcContextPool.Get()
	rc.Conn = w
	rc.Seq = msg.Seq
	rc.MethodName = msg.MethodName
	rc.ReqMd = msg.Md
	rc.marshaler = s.opt.MsgMarshaler

	var md *MethodDesc
	serviceInfo, ok := s.services[msg.ServiceName]
	if ok {
		md, ok = serviceInfo.methods[msg.MethodName]
		if !ok {
			rc.ReplyErr = errs.NotFindMethod.Print(msg.MethodName)
			rc.SerializeResponse()
			return
		}
	} else {
		rc.ReplyErr = errs.NotFindService.Print(msg.ServiceName)
		rc.SerializeResponse()
		return
	}

	rc.ReqMsg, rc.Handler = md.Handler(serviceInfo.serviceImpl)
	if err := s.opt.MsgUnmarshaler.Unmarshal(msg.Payload, rc.ReqMsg); err != nil {
		rc.ReplyErr = errs.Unmarshal.Print(err.Error())
		rc.SerializeResponse()
		return
	}

	s.opt.HandlerFunc(rc)
}

const msgLenSize = 4 //32bits uint32

type connectionContextKey string

const connectionContextName = connectionContextKey("ConnectionContext")

func (s *Server) prepare(conn netpoll.Connection) context.Context {
	w := newSvrWriter(conn, s.opt.WriteChanSize, s.ctx, &s.wg)
	s.conns.Store(conn, w)
	conn.AddCloseCallback(func(c netpoll.Connection) error {
		mlog.Infof("%s rpc server conn is closed %v", c.RemoteAddr().String(), c == conn)
		s.conns.Delete(c)
		w.stop()
		return nil
	})
	ctx := context.WithValue(s.ctx, connectionContextName, w)
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
	w := ctx.Value(connectionContextName).(*SvrWriter)
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

	s.handler(w, msg)
	return
}

type RpcContext struct {
	Conn       *SvrWriter
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
		mlog.Warnf("server conn is not active when serializing response %s", rc.MethodName)
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
	buf, _ := buffer.Malloc(msgLenSize + sz)
	data, err := rpcMsgMarshaler.MarshalAppend(buf[msgLenSize:msgLenSize], rsp) // len=0,cap=len(buf)-msgLenSize
	if err != nil {
		mlog.Errorf("rpc server serializeResponse proto.MarshalAppend %s err:%v\n", rc.MethodName, err)
		return
	}
	byteOrder.PutUint32(buf[:msgLenSize], uint32(len(data)))
	if len(data) == len(buf[msgLenSize:]) {
		mlog.Debugf("rpc server serializeResponse async send response %v", rc.Conn.c.IsActive())
		if err = rc.Conn.writeBuffer(buffer); err != nil {
			mlog.Errorf("rpc server serializeResponse writeBuffer err:%v\n", err)
			return
		}
	} else {
		mlog.Errorf("rpc server serializeResponse size wrong %s, %d, %d\n", rc.MethodName, len(data), len(buf[msgLenSize:]))
		return
	}
}

func newSvrWriter(conn netpoll.Connection, chanSize int, ctx context.Context, wg *sync.WaitGroup) *SvrWriter {
	w := &SvrWriter{
		c:         conn,
		writeChan: make(chan *netpoll.LinkBuffer, chanSize),
	}
	wg.Add(1)
	go w.writeLoop(ctx, wg)
	return w
}

type SvrWriter struct {
	c         netpoll.Connection
	writeChan chan *netpoll.LinkBuffer
	cancel    atomic.Value // context.CancelFunc
	mtx       sync.RWMutex
	closed    bool
}

func (w *SvrWriter) IsActive() bool {
	return w.c.IsActive()
}

func (w *SvrWriter) IsClosed() bool {
	return w.closed
}

func (w *SvrWriter) beforeClose() {
	w.mtx.Lock()
	defer w.mtx.Unlock()

	if w.closed {
		return
	}
	w.closed = true
	close(w.writeChan)
	if w.c.IsActive() {
		for b := range w.writeChan {
			w.c.Writer().Append(b)
		}
		if err := w.c.Writer().Flush(); err != nil {
			mlog.Errorf("SvrWriter beforeClose flush failed: %v", err)
		} else {
			mlog.Debugf("SvrWriter beforeClose flush success, %s", w.c.RemoteAddr().String())
		}
	}
}

func (w *SvrWriter) writeBuffer(buffer *netpoll.LinkBuffer) error {
	w.mtx.RLock()
	defer w.mtx.RUnlock()

	if !w.closed {
		w.writeChan <- buffer
	}
	return nil
}

func (w *SvrWriter) stop() {
	cancel, ok := w.cancel.Load().(context.CancelFunc)
	if ok {
		cancel()
	}
}

func (w *SvrWriter) writeLoop(ctx context.Context, wg *sync.WaitGroup) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)
	w.cancel.Store(cancel)
	defer func() {
		w.beforeClose()
		wg.Done()
	}()

	const maxBatchSize = 32 // netpoll barriercap = 32
	batch := 0
	flush := func() bool {
		if err := w.c.Writer().Flush(); err != nil {
			// closed、WriteTimeout、io error
			mlog.Errorf("SvrWriter Flush failed: %v", err)
			return false
		}
		batch = 0
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case buffer, ok := <-w.writeChan:
			if !ok {
				return
			}
			w.c.Writer().Append(buffer) // Append err is always nil
			batch++
			if len(w.writeChan) == 0 || batch >= maxBatchSize {
				mlog.Debugf("SvrWriter writeLoop flush buffer size: %d", batch)
				if !flush() {
					w.c.Close()
					return
				}
			}
		}
	}
}

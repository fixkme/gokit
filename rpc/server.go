package rpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"reflect"

	"hash/fnv"

	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

type MethodHandler func(srv any, ctx context.Context, dec func(in proto.Message) error) (proto.Message, error)

type MethodDesc struct {
	MethodName string
	Handler    MethodHandler
}

type ServiceDesc struct {
	ServiceName string
	HandlerType any
	Methods     []MethodDesc
}

type RpcContext struct {
	Req     *RpcRequestMessage
	Conn    gnet.Conn
	SrvImpl any
	Method  MethodHandler
}

type RpcHandler func(*RpcContext, ServerSerializer)

type ServerSerializer func(*RpcContext, proto.Message, error)

type Dispatcher func(gnet.Conn, *RpcRequestMessage, int) int

type Server struct {
	gnet.BuiltinEventEngine
	gnet.Engine
	services   map[string]*serviceInfo // service name -> service info
	processors []*rpcProcessor
	done       chan struct{}
	opt        *ServerOpt
}

type ServerOpt struct {
	gnet.Options
	Addr              string
	ProcessorSize     int64
	ProcessorTaskSize int64

	DispatcherFunc Dispatcher
	HandlerFunc    RpcHandler
}

func NewServer(opt *ServerOpt) *Server {
	s := &Server{
		services: make(map[string]*serviceInfo),
		done:     make(chan struct{}),
		opt:      opt,
	}
	if opt.HandlerFunc == nil {
		opt.HandlerFunc = func(rc *RpcContext, ser ServerSerializer) {
			dec := func(in proto.Message) error {
				return proto.Unmarshal(rc.Req.Payload, in)
			}
			reply, err := rc.Method(rc.SrvImpl, context.Background(), dec)
			ser(rc, reply, err)
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
	}
	return s
}

type serviceInfo struct {
	serviceImpl any
	methods     map[string]*MethodDesc
}

type rpcTask struct {
	conn gnet.Conn
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

func (s *Server) handler(c gnet.Conn, msg *RpcRequestMessage) {
	log.Printf("%d start handler rpc msg:%s\n", GoroutineID(), msg.String())
	ctx := new(RpcContext)
	ctx.Conn = c
	ctx.Req = msg

	var md *MethodDesc
	serviceInfo, ok := s.services[msg.ServiceName]
	if ok {
		md, ok = serviceInfo.methods[msg.MethodName]
		if !ok {
			err := fmt.Errorf("not found method:%s", msg.MethodName)
			s.serializeResponse(ctx, nil, err)
			return
		}
	} else {
		err := fmt.Errorf("not found service:%s", msg.ServiceName)
		s.serializeResponse(ctx, nil, err)
		return
	}

	ctx.SrvImpl = serviceInfo.serviceImpl
	ctx.Method = md.Handler

	// handler msg
	s.opt.HandlerFunc(ctx, s.serializeResponse)
	log.Printf("%d succeed handler rpc msg:%s\n", GoroutineID(), msg.String())
}

func (s *Server) serializeResponse(ctx *RpcContext, reply proto.Message, err error) {
	rsp := new(RpcResponseMessage)
	rsp.Seq = ctx.Req.Seq
	if err == nil {
		rspData, err := proto.Marshal(reply)
		if err == nil {
			rsp.Payload = rspData
		} else {
			rsp.Error = err.Error()
		}
	} else {
		rsp.Error = err.Error()
	}

	c := ctx.Conn
	outBuf, err := defaultMarshaler.Marshal(rsp)
	if err != nil {
		log.Printf("handler rpc msg proto.Marshal err:%v\n", err)
		return
	}
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(outBuf)))
	if _, err := c.Write(lenBuf); err != nil {
		log.Printf("handler rpc msg Write lenBuf err:%v\n", err)
		return
	}
	if _, err := c.Write(outBuf); err != nil {
		log.Printf("handler rpc msg Write outBuf err:%v\n", err)
		return
	}
}

func (s *Server) OnTraffic(c gnet.Conn) (r gnet.Action) {
	const lengthSize = 4 //32bits uint32
	for {
		log.Printf("%d server read cli buffer surplus size:%d", GoroutineID(), c.InboundBuffered())
		lenBuf, err := c.Peek(lengthSize)
		if err != nil {
			return gnet.None
		}
		dataLen := int(binary.BigEndian.Uint32(lenBuf))
		totalLen := lengthSize + dataLen
		bufferLen := c.InboundBuffered()
		if bufferLen < totalLen {
			return gnet.None
		}
		c.Discard(lengthSize)
		packetBuf, err := c.Next(dataLen)
		if err != nil {
			return gnet.None
		}
		// 反序列化
		msg := &RpcRequestMessage{}
		if err = defaultUnmarshaler.Unmarshal(packetBuf, msg); err != nil {
			log.Printf("proto.Unmarshal RpcRequestMessage err:%v\n", err)
			return gnet.None
		}

		if pn := len(s.processors); pn > 0 {
			// 分发消息
			var idx int
			if s.opt.DispatcherFunc != nil {
				idx = s.opt.DispatcherFunc(c, msg, pn)
			} else {
				h := fnv.New32a()
				h.Write([]byte(c.RemoteAddr().String()))
				idx = int(h.Sum32() % uint32(len(s.processors)))
			}
			s.processors[idx].inChan <- &rpcTask{conn: c, msg: msg}
		} else {
			// 直接处理
			s.handler(c, msg)
		}
	}
}

func (s *Server) makeRpcContext(c gnet.Conn, msg *RpcRequestMessage) (ctx *RpcContext) {
	ctx = &RpcContext{
		Req:  msg,
		Conn: c,
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

func (s *Server) Run() {
	for i := 0; i < len(s.processors); i++ {
		go s.processors[i].run(s.done)
	}
	if err := gnet.Run(s, s.opt.Addr, gnet.WithOptions(s.opt.Options)); err != nil {
		log.Fatalf("Server Run with error: %v", err)
	}
}

type rpcProcessor struct {
	server *Server
	inChan chan *rpcTask
}

func (p *rpcProcessor) run(done <-chan struct{}) {
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

package rpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"reflect"

	"hash/fnv"

	"github.com/fixkme/gokit/util/errs"
	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

type RpcContext_Gnet struct {
	Conn    gnet.Conn
	Req     *RpcRequestMessage
	SrvImpl any
	Method  MethodHandler

	Reply    proto.Message
	ReplyErr error
	ReplyMd  *Meta
}

type RpcHandler_Gnet func(*RpcContext_Gnet, ServerSerializer_Gnet)

type ServerSerializer_Gnet func(rc *RpcContext_Gnet, sync bool)

type DispatchHash_Gnet func(gnet.Conn, *RpcRequestMessage) int

type Server_Gnet struct {
	gnet.BuiltinEventEngine
	gnet.Engine
	services   map[string]*serviceInfo // service name -> service info
	processors []*rpcProcessor_Gnet
	done       chan struct{}
	opt        *ServerOpt_Gnet
}

type ServerOpt_Gnet struct {
	gnet.Options
	Addr              string
	ProcessorSize     int64
	ProcessorTaskSize int64

	DispatcherFunc DispatchHash_Gnet
	HandlerFunc    RpcHandler_Gnet
}

func NewServer_Gnet(opt *ServerOpt_Gnet) *Server_Gnet {
	s := &Server_Gnet{
		services: make(map[string]*serviceInfo),
		done:     make(chan struct{}),
		opt:      opt,
	}
	if opt.HandlerFunc == nil {
		opt.HandlerFunc = func(rc *RpcContext_Gnet, ser ServerSerializer_Gnet) {
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
			s.processors = append(s.processors, &rpcProcessor_Gnet{
				server: s,
				inChan: make(chan *rpcTask_Gnet, opt.ProcessorTaskSize),
			})
		}
	}
	return s
}

type rpcTask_Gnet struct {
	conn gnet.Conn
	msg  *RpcRequestMessage
}

func (s *Server_Gnet) RegisterService(sd *ServiceDesc, ss any) {
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

func (s *Server_Gnet) register(sd *ServiceDesc, ss any) {
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

func (s *Server_Gnet) handler(c gnet.Conn, msg *RpcRequestMessage) {
	log.Printf("%d start handler rpc msg:%s\n", GoroutineID(), msg.String())
	rc := new(RpcContext_Gnet)
	rc.Conn = c
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
	log.Printf("%d succeed handler rpc msg:%s\n", GoroutineID(), msg.String())
}

func (s *Server_Gnet) serializeResponse(rc *RpcContext_Gnet, sync bool) {
	rsp := new(RpcResponseMessage)
	rsp.Seq = rc.Req.Seq
	if rerr := rc.ReplyErr; rerr == nil {
		rspData, err := proto.Marshal(rc.Reply)
		if err == nil {
			rsp.Payload = rspData
		} else {
			rsp.Error = err.Error()
		}
	} else {
		if cerr, ok := rerr.(errs.CodeError); ok {
			rsp.Ecode = cerr.Code()
			rsp.Error = cerr.Error()
		} else {
			rsp.Error = rerr.Error()
		}
	}

	// 反序列化rpc rsp
	sz := defaultMarshaler.Size(rsp)
	buf := make([]byte, msgLenSize+sz)
	data, err := defaultMarshaler.MarshalAppend(buf[msgLenSize:msgLenSize], rsp)
	if err != nil {
		log.Printf("serializeResponse MarshalAppend wrong\n")
		return
	}
	if len(data) == len(buf[msgLenSize:]) {
		binary.LittleEndian.PutUint32(buf[:msgLenSize], uint32(len(data)))
		if !sync { //处于异步
			err = rc.Conn.AsyncWrite(buf, func(_ gnet.Conn, cerr error) error {
				if cerr != nil {
					log.Printf("AsyncWrite err:%v\n", err)
				}
				return nil
			})
		} else {
			_, err = rc.Conn.Write(buf)
		}
		if err != nil {
			log.Printf("serializeResponse Write wrong\n")
			return
		}
	} else {
		log.Printf("serializeResponse size wrong\n")
		return
	}
}

func (s *Server_Gnet) OnTraffic(c gnet.Conn) (r gnet.Action) {
	for {
		log.Printf("%d server read cli buffer surplus size:%d", GoroutineID(), c.InboundBuffered())
		lenBuf, err := c.Peek(msgLenSize)
		if err != nil {
			return gnet.None
		}
		dataLen := int(binary.LittleEndian.Uint32(lenBuf))
		totalLen := msgLenSize + dataLen
		bufferLen := c.InboundBuffered()
		if bufferLen < totalLen {
			return gnet.None
		}
		c.Discard(msgLenSize)
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
				hashCode := s.opt.DispatcherFunc(c, msg)
				idx = hashCode % pn
			} else {
				h := fnv.New32a()
				h.Write([]byte(c.RemoteAddr().String()))
				idx = int(h.Sum32() % uint32(len(s.processors)))
			}
			s.processors[idx].inChan <- &rpcTask_Gnet{conn: c, msg: msg}
		} else {
			// 直接处理
			s.handler(c, msg)
		}
	}
}

func (s *Server_Gnet) makeRpcContext(c gnet.Conn, msg *RpcRequestMessage) (ctx *RpcContext_Gnet) {
	ctx = &RpcContext_Gnet{
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

func (s *Server_Gnet) Run() {
	for i := 0; i < len(s.processors); i++ {
		go s.processors[i].run(s.done)
	}
	if err := gnet.Run(s, s.opt.Addr, gnet.WithOptions(s.opt.Options)); err != nil {
		log.Fatalf("Server Run with error: %v", err)
	}
}

type rpcProcessor_Gnet struct {
	server *Server_Gnet
	inChan chan *rpcTask_Gnet
}

func (p *rpcProcessor_Gnet) run(done <-chan struct{}) {
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

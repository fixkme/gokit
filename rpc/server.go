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

type MethodHandler func(srv any, ctx context.Context, in []byte) (proto.Message, error)

type MethodDesc struct {
	MethodName string
	Handler    MethodHandler
}

type ServiceDesc struct {
	ServiceName string
	HandlerType any
	Methods     []MethodDesc
}

type MsgHandler func(c gnet.Conn, msg *RpcRequestMessage)

type ServerInterceptor func(ctx context.Context, req any, info *RpcRequestMessage, handler MsgHandler) (resp any, err error)

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
	Addr          string
	ProcessorSize int64
}

func NewServer(opt *ServerOpt) *Server {
	s := &Server{
		services: make(map[string]*serviceInfo),
		done:     make(chan struct{}),
		opt:      opt,
	}
	if opt.ProcessorSize > 0 {
		// 异步模式
		for i := 0; i < int(opt.ProcessorSize); i++ {
			s.processors = append(s.processors, &rpcProcessor{
				server: s,
				inChan: make(chan *rpcClient, 1024),
			})
		}
	}
	return s
}

type serviceInfo struct {
	serviceImpl any
	methods     map[string]*MethodDesc
}

type rpcClient struct {
	cli gnet.Conn
	msg *RpcRequestMessage
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
	defer func() {
		if err := recover(); err != nil {
			log.Printf("handler rpc msg panic,%s, stack:%v\n", msg.String(), err)
		}
	}()

	rsp := &RpcResponseMessage{}

	var md *MethodDesc
	serviceInfo, ok := s.services[msg.ServiceName]
	if ok {
		md, ok = serviceInfo.methods[msg.MethodName]
		if !ok {
			rsp.Error = fmt.Sprintf("not found method:%s", msg.MethodName)
		}
	} else {
		rsp.Error = fmt.Sprintf("not found service:%s", msg.ServiceName)
	}

	// handler msg
	if md != nil {
		ctx := c.Context().(context.Context)
		reply, err := md.Handler(serviceInfo.serviceImpl, ctx, msg.Payload)
		if err != nil {
			rsp.Error = err.Error()
		} else {
			rspData, err := proto.Marshal(reply)
			if err != nil {
				rsp.Error = err.Error()
			} else {
				rsp.Payload = rspData
			}
		}
	}

	// 序列化
	outBuf, err := proto.Marshal(rsp)
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
	return
}

func (s *Server) OnTraffic(c gnet.Conn) (r gnet.Action) {
	const lengthSize = 4 //32bits uint32
	for {
		lenBuf, err := c.Peek(lengthSize)
		if err != nil {
			return gnet.None
		}
		dataLen := binary.BigEndian.Uint32(lenBuf)
		totalLen := lengthSize + int(dataLen)
		bufferLen := c.InboundBuffered()
		if totalLen < bufferLen {
			return gnet.None
		}
		packetBuf, err := c.Next(totalLen)
		if err != nil {
			return gnet.None
		}
		// 反序列化
		msg := &RpcRequestMessage{}
		if err = proto.Unmarshal(packetBuf[lengthSize:], msg); err != nil {
			log.Printf("proto.Unmarshal RpcRequestMessage err:%v\n", err)
			return gnet.None
		}

		if len(s.processors) > 0 {
			// 分发消息
			var idx int
			if msg.Target > 0 {
				idx = int(msg.Target) % len(s.processors)
			} else {
				h := fnv.New32a()
				h.Write([]byte(c.RemoteAddr().String()))
				idx = int(h.Sum32() % uint32(len(s.processors)))
			}
			s.processors[idx].inChan <- &rpcClient{c, msg}
		} else {
			// 直接处理
			s.handler(c, msg)
		}
		return gnet.None
	}
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
	inChan chan *rpcClient
}

func (p *rpcProcessor) run(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case v, ok := <-p.inChan:
			if ok {
				p.server.handler(v.cli, v.msg)
			}
		}
	}
}

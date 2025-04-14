package rpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"

	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

type MethodHandler func(srv any, ctx context.Context, in proto.Message) (any, error)

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

type Server struct {
	gnet.BuiltinEventEngine
	services   map[string]*serviceInfo // service name -> service info
	processors []*rpcProcessor
}

type ServerOpt struct {
	ProcessorSize int64
}

func NewServer(opt *ServerOpt) *Server {
	s := &Server{
		services: make(map[string]*serviceInfo),
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
	serviceInfo, ok := s.services[msg.ServiceName]
	if !ok {
		return
	}
	md, ok := serviceInfo.methods[msg.MethodName]
	if !ok {
		return
	}
	reply, err := md.Handler(serviceInfo.serviceImpl, c.Context().(context.Context), msg.Payload)
	//c.AsyncWrite()
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
		msgBuf, err := c.Next(totalLen)
		if err != nil {
			return gnet.None
		}
		// 反序列化
		msg := &RpcRequestMessage{}
		if err = proto.Unmarshal(msgBuf, msg); err != nil {
			return gnet.None
		}
		// 分发消息
		if len(s.processors) > 0 {

		}
		return gnet.None
	}
}

func (s *Server) Run() {
	for i := 0; i < int(s.processorSize); i++ {
		go func() {
			for {
				select {
				case v, ok := <-s.inChan:
					if ok {
						s.handler(v.cli, v.msg)
					}
				}
			}
		}()
	}
}

type rpcProcessor struct {
	server *Server
	inChan chan *rpcClient
}

func (p *rpcProcessor) run() {
	for {
		select {
		case v, ok := <-p.inChan:
			if ok {
				p.server.handler(v.cli, v.msg)
			}
		}
	}
}

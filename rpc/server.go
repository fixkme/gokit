package rpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
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

type serviceInfo struct {
	serviceImpl any
	methods     map[string]*MethodDesc
}

type Server struct {
	gnet.BuiltinEventEngine
	services map[string]*serviceInfo // service name -> service info
}

func (s *Server) RegisterService(sd *ServiceDesc, ss any) {
	if ss != nil {
		ht := reflect.TypeOf(sd.HandlerType).Elem()
		st := reflect.TypeOf(ss)
		if !st.Implements(ht) {
			err := fmt.Errorf("grpc: Server.RegisterService found the handler of type %v that does not satisfy %v", st, ht)
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

type RpcMessage struct {
	ServiceName string
	MethodName  string
	MessageName string //full name
	Payload     []byte //data
	ErrStr      string //rsp error
}

func (s *Server) OnTraffic(c gnet.Conn) (r gnet.Action) {
	const lengthSize = 4 //32bits uint32
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
	msg := &RpcMessage{}
	if err = json.Unmarshal(msgBuf, msg); err != nil {
		return gnet.None
	}

	return gnet.None
}

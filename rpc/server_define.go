package rpc

import (
	"context"

	"google.golang.org/protobuf/proto"
)

type ServiceRegistrar interface {
	RegisterService(desc *ServiceDesc, impl any)
}

type ServerInterface interface {
	ServiceRegistrar
	Run() error
	Stop(context.Context) error
}

type Handler func(ctx context.Context, req proto.Message) (proto.Message, error)

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

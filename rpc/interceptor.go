package rpc

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// type Invoker func(ctx context.Context, method string, req, reply any, cc *ClientConn, opts ...CallOption) error

// type ClientInterceptor func(ctx context.Context, method string, req, reply any, cc *ClientConn, invoker Invoker, opts ...CallOption) error

type CallOption struct {
}

type Handler func(ctx context.Context, req proto.Message) (proto.Message, error)

//type ServerInterceptor func(ctx context.Context, req any, rc *RpcContext, handler Handler) (resp any, err error)

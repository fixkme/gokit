package rpc

import "context"

// type Invoker func(ctx context.Context, method string, req, reply any, cc *ClientConn, opts ...CallOption) error

// type ClientInterceptor func(ctx context.Context, method string, req, reply any, cc *ClientConn, invoker Invoker, opts ...CallOption) error

type CallOption struct {
}

type Handler func(ctx context.Context, req any) (any, error)

type ServerInterceptor func(ctx context.Context, req any, info *RpcContext, handler Handler) (resp any, err error)

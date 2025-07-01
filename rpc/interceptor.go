package rpc

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
)

//type Invoker func(ctx context.Context, method string, req, reply proto.Message, cc *Connection, opts ...CallOption) error

// type ClientInterceptor func(ctx context.Context, method string, req, reply proto.Message, cc *Connection, invoker Invoker, opts ...CallOption) error

type CallOption struct {
	Sync         bool //同步调用
	Timeout      time.Duration
	AsyncRetChan chan *AsyncCallResult
	Md           *Meta
}

type AsyncCallResult struct {
	Err error
	Rsp proto.Message
}

type callResult struct {
	senderr error
	rpcRsp  *RpcResponseMessage
}

type Handler func(ctx context.Context, req proto.Message) (proto.Message, error)

//type ServerInterceptor func(ctx context.Context, req any, rc *RpcContext, handler Handler) (resp any, err error)

package rpc

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
)

//type Invoker func(ctx context.Context, method string, req, reply proto.Message, cc *Connection, opts ...CallOption) error

// type ClientInterceptor func(ctx context.Context, method string, req, reply proto.Message, cc *Connection, invoker Invoker, opts ...CallOption) error

type CallOption struct {
	Async        bool                  //是否异步，默认同步调用
	Timeout      time.Duration         //同步、异步都有效
	AsyncRetChan chan *AsyncCallResult //异步调用的输出，不关注回应的话设置为nil
	ReqMd        *Meta                 //其他请求数据
	PassThrough  any                   //异步调用的透传数据
}

type CallResult struct {
	Err   error //出错
	Rsp   any   //proto.Message或[]byte
	RspMd *Meta //回应的元数据
}

type AsyncCallResult struct {
	Err         error //出错
	Rsp         any   //proto.Message或[]byte
	RspMd       *Meta //回应的元数据
	PassThrough any   //CallOption.PassThrough
}

type callResult struct {
	senderr error
	rpcRsp  *RpcResponseMessage
}

type Handler func(ctx context.Context, req proto.Message) (proto.Message, error)

//type ServerInterceptor func(ctx context.Context, req any, rc *RpcContext, handler Handler) (resp any, err error)

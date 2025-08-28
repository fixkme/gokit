package rpc

import (
	"context"
	"time"

	"github.com/cloudwego/netpoll"
	"google.golang.org/protobuf/proto"
)

type ClientConnInterface interface {
	// req：支持proto.Message和[]byte
	// outRsp：req对应的response，调用者明确指定具体的proto.Message对象，该方法不会帮助创建response对象
	// rspData: 同步调用且outRsp为nil时，以[]byte返回rspData
	// 默认是同步调用
	Invoke(ctx context.Context, service, method string, req any, outRsp proto.Message, opts ...*CallOption) (rspMd *Meta, rspData []byte, err error)
}

type ClientOpt struct {
	DailTimeout   time.Duration
	OnClientClose func(netpoll.Connection) error
	Marshaler     *proto.MarshalOptions
	Unmarshaler   *proto.UnmarshalOptions
}

type CallOption struct {
	Async        bool                  //是否异步，默认同步调用
	Timeout      time.Duration         //同步、异步都有效
	AsyncRetChan chan *AsyncCallResult //异步调用的输出，不关注回应的话设置为nil
	ReqMd        *Meta                 //其他请求数据
	PassThrough  any                   //异步调用的透传数据
}

type CallResult struct {
	Err     error //出错
	RspData any   //proto.Message或[]byte
	RspMd   *Meta //回应的元数据
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

type callInfo struct {
	syncRet     chan *callResult
	asyncRet    chan *AsyncCallResult
	outRsp      proto.Message
	passThrough any   //异步调用的透传数据
	expireAt    int64 //异步调用超时时间
}

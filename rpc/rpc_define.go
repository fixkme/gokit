package rpc

import (
	"context"

	"google.golang.org/protobuf/proto"
)

type RPCReq func(ctx context.Context, cc *ClientConn) (resp proto.Message, err error)

type RpcClient interface {
	Call(serviceName string, cb RPCReq) (resp proto.Message, err error)
}

type RPCReg func(serv ServiceRegistrar, nodeName string) error

type RpcServer interface {
	RegisterService(serviceName string, cb RPCReg) error
}

package rpc

import (
	sync "sync"
)

type _RpcContextPool struct {
	sync.Pool
}

var rpcContextPool = newRpcContextPool()

func newRpcContextPool() *_RpcContextPool {
	return &_RpcContextPool{
		Pool: sync.Pool{New: func() any {
			return new(RpcContext)
		}},
	}
}

func (p *_RpcContextPool) Get() *RpcContext {
	rc := p.Pool.Get().(*RpcContext)
	return rc
}

func (p *_RpcContextPool) Put(rc *RpcContext) {
	p.Pool.Put(rc)
	rc.Conn = nil
	rc.MethodName = ""
	rc.ReqMd = nil
	rc.ReqMsg = nil
	rc.Handler = nil
	rc.Reply = nil
	rc.ReplyErr = nil
	rc.ReplyMd = nil
}

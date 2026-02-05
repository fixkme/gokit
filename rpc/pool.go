package rpc

import (
	sync "sync"
)

type RpcContextPool struct {
	sync.Pool
}

var rpcContextPool = &RpcContextPool{
	Pool: sync.Pool{New: func() interface{} {
		return new(RpcContext)
	}},
}

func (p *RpcContextPool) Get() *RpcContext {
	rc := p.Pool.Get().(*RpcContext)
	return rc
}

func (p *RpcContextPool) Put(rc *RpcContext) {
	p.Pool.Put(rc)
	rc.Conn = nil
	rc.Req = nil
	rc.SrvImpl = nil
	rc.Method = nil
	rc.Reply = nil
	rc.ReplyErr = nil
	rc.ReplyMd = nil
}

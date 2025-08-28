package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	sync "sync"
	"sync/atomic"
	"time"

	"github.com/fixkme/gokit/mlog"
	"github.com/fixkme/gokit/util/errs"
	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

type ConnState struct {
	c        gnet.Conn
	genMsgId atomic.Uint32
	waitRsps map[uint32]chan *callResult
	mtx      sync.Mutex
}

func Invoke(cs *ConnState, ctx context.Context, path string, req, rsp proto.Message, opts ...*CallOption) error {
	var opt *CallOption
	if len(opts) != 0 {
		opt = opts[0]
	} else {
		opt = &CallOption{}
	}

	// 构造请求消息
	v2 := strings.SplitN(path, "/", 2)
	if len(v2) != 2 {
		return fmt.Errorf("invalid path: %s", path)
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return err
	}

	seq := cs.genMsgId.Add(1)
	rpcReq := &RpcRequestMessage{
		Seq:         seq,
		ServiceName: v2[0],
		MethodName:  v2[1],
		Md:          opt.ReqMd,
		Payload:     payload,
	}
	datas, err := proto.Marshal(rpcReq)
	if err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	byteOrder.PutUint32(lenBuf, uint32(len(datas)))
	if opt.Async {
		// TODO 异步发送，如果io协程里写入fd失败是无法知道的
		return cs.c.AsyncWritev([][]byte{lenBuf, datas}, nil)
	}

	// 同步
	retCh := make(chan *callResult, 1)
	cs.mtx.Lock()
	cs.waitRsps[seq] = retCh
	cs.mtx.Unlock()
	defer func() {
		cs.mtx.Lock()
		delete(cs.waitRsps, seq)
		cs.mtx.Unlock()
	}()

	err = cs.c.AsyncWritev([][]byte{lenBuf, datas}, func(c gnet.Conn, serr error) error {
		if serr != nil {
			cs := c.Context().(*ConnState)
			cs.mtx.Lock()
			defer cs.mtx.Unlock()
			rch, ok := cs.waitRsps[seq]
			if ok {
				rch <- &callResult{senderr: serr}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if opt.Timeout > 0 {
		subctx, cancel := context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
		ctx = subctx
	}
	select {
	case <-ctx.Done():
		select {
		case ret := <-retCh:
			if ret.senderr != nil {
				return ret.senderr
			}
			if ret.rpcRsp == nil {
				// 发送成功，但是还没有结果
				return errors.New("wait rsp exceed")
			}
			return cs.decodeRpcRsp(ret.rpcRsp, rsp)
		default:
			return ctx.Err()
		}
	case ret := <-retCh:
		if ret.senderr != nil {
			return ret.senderr
		}
		return cs.decodeRpcRsp(ret.rpcRsp, rsp)
	}
	return nil
}

func (c *ConnState) decodeRpcRsp(rpcRsp *RpcResponseMessage, out proto.Message) error {
	if rpcRsp.Error != "" {
		if rpcRsp.Ecode != 0 {
			return errs.CreateCodeError(rpcRsp.Ecode, rpcRsp.Error)
		}
		return errors.New(rpcRsp.Error)
	}
	if out != nil {
		if err := proto.Unmarshal(rpcRsp.Payload, out); err != nil {
			mlog.Error("failed to unmarshal response: %v", err)
			return err
		}
	}
	return nil
}

type ClientHander struct {
}

func (h *ClientHander) OnBoot(eng gnet.Engine) (action gnet.Action) {
	mlog.Debug("OnBoot")
	return 0
}

// OnShutdown fires when the engine is being shut down, it is called right after
// all event-loops and connections are closed.
func (h *ClientHander) OnShutdown(eng gnet.Engine) {
	mlog.Debug("OnShutdown")
}

// OnOpen fires when a new connection has been opened.
//
// The Conn c has information about the connection such as its local and remote addresses.
// The parameter out is the return value which is going to be sent back to the remote.
// Sending large amounts of data back to the remote in OnOpen is usually not recommended.
func (h *ClientHander) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	mlog.Debug("OnOpen")
	cs := &ConnState{
		c:        c,
		waitRsps: make(map[uint32]chan *callResult),
	}
	c.SetContext(cs)
	return nil, gnet.None
}

// OnClose fires when a connection has been closed.
// The parameter err is the last known connection error.
func (h *ClientHander) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	mlog.Debug("OnClose")
	return 0
}

// OnTraffic fires when a socket receives data from the remote.
//
// Note that the []byte returned from Conn.Peek(int)/Conn.Next(int) is not allowed to be passed to a new goroutine,
// as this []byte will be reused within event-loop after OnTraffic() returns.
// If you have to use this []byte in a new goroutine, you should either make a copy of it or call Conn.Read([]byte)
// to read data into your own []byte, then pass the new []byte to the new goroutine.
func (h *ClientHander) OnTraffic(c gnet.Conn) (action gnet.Action) {
	mlog.Debug("OnTraffic")
	for {
		lenBuf := make([]byte, 4)
		_, err := c.Read(lenBuf)
		if err != nil {
			return
		}
		dataLen := byteOrder.Uint32(lenBuf)
		msgBuf := make([]byte, dataLen)
		_, err = c.Read(msgBuf)
		if err != nil {
			return
		}
		// 反序列化
		msg := &RpcResponseMessage{}
		if err = proto.Unmarshal(msgBuf, msg); err != nil {
			return
		}

		fmt.Printf("rpc rsp:%v\n", msg)

		cs := c.Context().(*ConnState)
		cs.mtx.Lock()
		rch, ok := cs.waitRsps[msg.Seq]
		cs.mtx.Unlock()
		if ok {
			rch <- &callResult{rpcRsp: msg}
		}
	}
}

// OnTick fires immediately after the engine starts and will fire again
// following the duration specified by the delay return value.
func (h *ClientHander) OnTick() (delay time.Duration, action gnet.Action) {
	mlog.Debug("OnTick")
	return 5 * time.Second, 0
}

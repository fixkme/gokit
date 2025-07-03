package rpc

import (
	"context"
	"errors"
	sync "sync"
	"sync/atomic"
	"time"

	"github.com/fixkme/gokit/log"

	"github.com/cloudwego/netpoll"
	"github.com/cloudwego/netpoll/mux"
	"github.com/emirpasic/gods/trees/redblacktree"
	"github.com/fixkme/gokit/util/errs"
	g "github.com/fixkme/gokit/util/go"
	"google.golang.org/protobuf/proto"
)

type ClientConn struct {
	conn     netpoll.Connection
	wqueue   *mux.ShardQueue
	genMsgId atomic.Uint32
	waitRsps map[uint32]*callInfo
	mtx      sync.Mutex

	timeouts        *redblacktree.Tree // expireAt => set[msgId]
	tmoutMtx        sync.Mutex
	runTimeout      atomic.Bool
	quit            chan struct{}
	updateTimerTrig chan int64
}

type callInfo struct {
	syncRet     chan *callResult
	asyncRet    chan *AsyncCallResult
	outRsp      proto.Message
	passThrough any   //异步调用的透传数据
	expireAt    int64 //异步调用超时时间
}

type ClientOpt struct {
	DailTimeout    time.Duration
	ShardQueueSize int
	OnClientClose  func(netpoll.Connection) error
}

func NewClientConn(network, address string, opt *ClientOpt) (*ClientConn, error) {
	if opt.ShardQueueSize == 0 {
		opt.ShardQueueSize = mux.ShardSize
	}
	if opt.OnClientClose == nil {
		opt.OnClientClose = onClientClose
	}
	conn, err := netpoll.DialConnection(network, address, opt.DailTimeout)
	if err != nil {
		return nil, err
	}
	cliConn := &ClientConn{
		wqueue:          mux.NewShardQueue(opt.ShardQueueSize, conn),
		waitRsps:        make(map[uint32]*callInfo),
		timeouts:        &redblacktree.Tree{Comparator: timeUnixComparator},
		quit:            make(chan struct{}, 1),
		updateTimerTrig: make(chan int64, 1),
	}
	conn.SetOnRequest(func(ctx context.Context, connection netpoll.Connection) error {
		return onClientMsg(ctx, connection, cliConn)
	})
	conn.AddCloseCallback(opt.OnClientClose)
	conn.AddCloseCallback(func(connection netpoll.Connection) error {
		close(cliConn.quit)
		return nil
	})
	cliConn.conn = conn
	return cliConn, nil
}

var defaultCallOption = &CallOption{}

func (cs *ClientConn) Invoke(ctx context.Context, service, method string, req any, rsp proto.Message, opts ...*CallOption) (rspData []byte, err error) {
	var opt *CallOption
	if len(opts) != 0 {
		opt = opts[0]
	} else {
		opt = defaultCallOption
	}
	// 构造请求消息
	var payload []byte
	if reqPb, ok := req.(proto.Message); ok {
		payload, err = proto.Marshal(reqPb)
		if err != nil {
			return nil, err
		}
	} else {
		payload = req.([]byte)
	}
	seq := cs.genMsgId.Add(1)
	rpcReq := &RpcRequestMessage{
		Seq:         seq,
		ServiceName: service,
		MethodName:  method,
		Md:          opt.ReqMd,
		Payload:     payload,
	}
	buffer, err := cs.encodeRpcReq(rpcReq)
	if err != nil {
		return nil, err
	}
	cs.AsyncWrite(buffer)

	// 处理异步
	if opt.Async {
		if ch := opt.AsyncRetChan; ch != nil {
			var expireAt int64
			if opt.Timeout > 0 {
				expireAt = time.Now().UnixMilli() + int64(opt.Timeout.Milliseconds())
				cs.tmoutMtx.Lock()
				val, ok := cs.timeouts.Get(expireAt)
				var set map[uint32]struct{}
				if ok {
					set = val.(map[uint32]struct{})
				} else {
					set = make(map[uint32]struct{}, 1)
					cs.timeouts.Put(expireAt, set)
				}
				set[seq] = struct{}{}
				cs.tmoutMtx.Unlock()
				//fmt.Printf("seq %d deadline %v\n", seq, expireAt)
				if !cs.runTimeout.Load() {
					cs.runTimeout.Store(true)
					go cs.processTimeout(cs.quit)
				} else {
					cs.updateTimerTrig <- expireAt
				}
			}
			callInfo := &callInfo{asyncRet: ch, outRsp: rsp, expireAt: expireAt, passThrough: opt.PassThrough}
			cs.mtx.Lock()
			cs.waitRsps[seq] = callInfo
			cs.mtx.Unlock()
		}
		return nil, nil
	}

	// 处理同步
	syncRet := make(chan *callResult, 1)
	callInfo := &callInfo{syncRet: syncRet}
	cs.mtx.Lock()
	cs.waitRsps[seq] = callInfo
	cs.mtx.Unlock()
	defer func() {
		close(syncRet)
		cs.mtx.Lock()
		delete(cs.waitRsps, seq)
		cs.mtx.Unlock()
	}()

	if opt.Timeout > 0 {
		subctx, cancel := context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
		ctx = subctx
	}
	select {
	case <-ctx.Done():
		select {
		case ret := <-syncRet:
			if ret.senderr != nil {
				return nil, ret.senderr
			}
			if ret.rpcRsp == nil {
				// 发送成功，但是还没有结果, 也许还在返回的路上
				return nil, errors.New("wait reply exceed")
			}
			return cs.decodeRpcRsp(ret.rpcRsp, rsp)
		default:
			return nil, ctx.Err()
		}
	case ret := <-syncRet:
		if ret.senderr != nil {
			return nil, ret.senderr
		}
		return cs.decodeRpcRsp(ret.rpcRsp, rsp)
	}
}

func (c *ClientConn) AsyncWrite(buffer *netpoll.LinkBuffer) {
	c.wqueue.Add(func() (buf netpoll.Writer, isNil bool) {
		return buffer, false
	})
}

func (c *ClientConn) encodeRpcReq(rpcReq *RpcRequestMessage) (*netpoll.LinkBuffer, error) {
	sz := defaultMarshaler.Size(rpcReq)
	buffer := netpoll.NewLinkBuffer(msgLenSize + sz)
	// 获取整个写入空间(长度头+消息体)
	buf, err := buffer.Malloc(msgLenSize + sz)
	if err != nil {
		return nil, err
	}
	data, err := defaultMarshaler.MarshalAppend(buf[msgLenSize:msgLenSize], rpcReq)
	if err != nil {
		return nil, err
	}
	byteOrder.PutUint32(buf[:msgLenSize], uint32(len(data)))
	if len(data) != len(buf[msgLenSize:]) {
		return nil, errors.New("proto.MarshalAppend size is wrong")
	}
	return buffer, nil
}

func (c *ClientConn) decodeRpcRsp(rpcRsp *RpcResponseMessage, out proto.Message) (rspData []byte, err error) {
	if rpcRsp.Error != "" {
		if rpcRsp.Ecode != 0 {
			return nil, errs.CreateCodeError(rpcRsp.Ecode, rpcRsp.Error)
		}
		return nil, errors.New(rpcRsp.Error)
	}
	if out != nil {
		if err := proto.Unmarshal(rpcRsp.Payload, out); err != nil {
			log.Error("rpc client failed to unmarshal response: %v", err)
			return nil, err
		}
	}
	return rpcRsp.Payload, nil
}

func onClientClose(conn netpoll.Connection) error {
	log.Info("%s rpc client is Closed", conn.RemoteAddr().String())
	return nil
}

func onClientMsg(ctx context.Context, conn netpoll.Connection, cli *ClientConn) (err error) {
	reader := conn.Reader()
	log.Info("%d client read buffer surplus size:%d", g.GoroutineID(), reader.Len())
	for {
		lenBuf, _err := reader.Peek(msgLenSize)
		if _err != nil {
			return _err
		}
		dataLen := int(byteOrder.Uint32(lenBuf))
		totalLen := msgLenSize + dataLen
		if reader.Len() < totalLen {
			return
		}
		if err = reader.Skip(msgLenSize); err != nil {
			return
		}
		packetBuf, _err := reader.Next(dataLen)
		if _err != nil {
			return _err
		}
		// 反序列化
		msg := &RpcResponseMessage{}
		if err = defaultUnmarshaler.Unmarshal(packetBuf, msg); err != nil {
			log.Error("proto.Unmarshal RpcRequestMessage err:%v\n", err)
			return err
		}

		cli.mtx.Lock()
		callInfo, ok := cli.waitRsps[msg.Seq]
		cli.mtx.Unlock()
		if ok {
			if callInfo.syncRet != nil {
				callInfo.syncRet <- &callResult{rpcRsp: msg}
			} else if callInfo.asyncRet != nil {
				if exAt := callInfo.expireAt; exAt > 0 {
					cli.tmoutMtx.Lock()
					val, ok := cli.timeouts.Get(exAt)
					if ok {
						set := val.(map[uint32]struct{})
						delete(set, msg.Seq)
					}
					cli.tmoutMtx.Unlock()
				}

				cli.mtx.Lock()
				delete(cli.waitRsps, msg.Seq)
				cli.mtx.Unlock()

				rspData, callErr := cli.decodeRpcRsp(msg, callInfo.outRsp)
				asyncRet := &AsyncCallResult{Err: callErr, RspMd: msg.Md, PassThrough: callInfo.passThrough}
				if callInfo.outRsp != nil {
					asyncRet.Rsp = callInfo.outRsp
				} else {
					asyncRet.Rsp = rspData
				}
				callInfo.asyncRet <- asyncRet
			}
		}
	}
}

func (c *ClientConn) processTimeout(quit <-chan struct{}) {
	nowMs := time.Now().UnixMilli()
	c.tmoutMtx.Lock()
	expireAt := c.timeouts.Left().Key.(int64)
	c.tmoutMtx.Unlock()
	dur := expireAt - nowMs
	timer := time.NewTimer(time.Millisecond * time.Duration(dur))
	defer timer.Stop()
	var wasEmpty, updateTimer bool
	for {
		select {
		case <-quit:
			return
		case newExpireAt := <-c.updateTimerTrig:
			updateTimer = false
			c.tmoutMtx.Lock()
			expireAt = c.timeouts.Left().Key.(int64)
			c.tmoutMtx.Unlock()
			if wasEmpty {
				wasEmpty = false
				updateTimer = true
			} else {
				if newExpireAt < expireAt {
					updateTimer = true
				}
			}
			if updateTimer {
				nowMs = time.Now().UnixMilli()
				dur = expireAt - nowMs
				timer.Reset(time.Millisecond * time.Duration(dur))
			}
		case <-timer.C:
			nowMs = time.Now().UnixMilli()
			callInfos := make([]*callInfo, 0)
			removes := make([]int64, 0)
			c.tmoutMtx.Lock()
			it := c.timeouts.Iterator()
			for it.Next() {
				expireAt = it.Key().(int64)
				if nowMs < expireAt {
					dur = expireAt - nowMs
					timer.Reset(time.Millisecond * time.Duration(dur))
					break
				}
				removes = append(removes, expireAt)
				set := it.Value().(map[uint32]struct{})
				c.mtx.Lock()
				for seq := range set {
					callInfo, ok := c.waitRsps[seq]
					if ok {
						//fmt.Printf("seq %d is timeout %d\n", seq, nowMs)
						delete(c.waitRsps, seq)
						callInfos = append(callInfos, callInfo)
					}
				}
				c.mtx.Unlock()
			}
			for _, expireAt := range removes {
				c.timeouts.Remove(expireAt)
			}
			if c.timeouts.Size() > 0 {
				nowMs = time.Now().UnixMilli()
				expireAt = c.timeouts.Left().Key.(int64)
				dur = expireAt - nowMs
				timer.Reset(time.Millisecond * time.Duration(dur))
			} else {
				wasEmpty = true
				timer.Reset(time.Minute * 5)
			}
			c.tmoutMtx.Unlock()
			for _, callInfo := range callInfos {
				if callInfo.asyncRet != nil {
					callInfo.asyncRet <- &AsyncCallResult{Err: errors.New("rpc async call timeout")}
				}
			}
		}
	}
}

func timeUnixComparator(a, b interface{}) int {
	aAsserted := a.(int64)
	bAsserted := b.(int64)
	switch {
	case aAsserted > bAsserted:
		return 1
	case aAsserted < bAsserted:
		return -1
	default:
		return 0
	}
}

package rpc

import (
	"context"
	"errors"
	sync "sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
	"github.com/emirpasic/gods/trees/redblacktree"
	"github.com/fixkme/gokit/mlog"
	"github.com/fixkme/gokit/util"
	"google.golang.org/protobuf/proto"
)

type ClientConn struct {
	conn     netpoll.Connection
	network  string
	address  string
	opt      *ClientOpt
	genMsgId atomic.Uint32
	waitRsps map[uint32]*callInfo
	mtx      sync.Mutex

	timeouts        *redblacktree.Tree // expireAt => set[msgId]
	tmoutMtx        sync.Mutex
	runTimeout      atomic.Bool
	quit            chan struct{}
	updateTimerTrig chan int64
}

func NewClientConn(network, address string, opt *ClientOpt) (*ClientConn, error) {
	if opt == nil {
		opt = defaultClientOpt
	} else {
		initClientOpt(opt)
	}

	conn, err := netpoll.DialConnection(network, address, opt.DailTimeout)
	if err != nil {
		return nil, err
	}
	cliConn := &ClientConn{
		conn:            conn,
		network:         network,
		address:         address,
		opt:             opt,
		waitRsps:        make(map[uint32]*callInfo),
		timeouts:        &redblacktree.Tree{Comparator: timeUnixComparator},
		quit:            make(chan struct{}),
		updateTimerTrig: make(chan int64, 1),
	}
	cliConn.initConn(conn)
	return cliConn, nil
}

func initClientOpt(opt *ClientOpt) {
	if opt.Marshaler == nil {
		opt.Marshaler = &proto.MarshalOptions{}
	}
	if opt.Unmarshaler == nil {
		opt.Unmarshaler = &proto.UnmarshalOptions{RecursionLimit: 100}
	}
}

var (
	defaultClientOpt = &ClientOpt{
		DailTimeout: time.Second * 5,
		Marshaler:   &proto.MarshalOptions{},
		Unmarshaler: &proto.UnmarshalOptions{RecursionLimit: 100},
	}
	defaultCallOption  = &CallOption{}
	ErrInvalidReqData  = errors.New("invalid request data")
	ErrWaitReplyExceed = errors.New("wait reply exceed")
)

func (c *ClientConn) Close() error {
	close(c.quit)
	return c.conn.Close()
}

// req：支持proto.Message和[]byte
// outRsp：req对应的response，调用者明确指定具体的proto.Message对象，该方法不会帮助创建response对象
// rspData: 同步调用且outRsp为nil时，以[]byte返回rspData
// 默认是同步调用
func (c *ClientConn) Invoke(ctx context.Context, service, method string, req any, outRsp proto.Message, opts ...*CallOption) (rspMd *Meta, rspData []byte, err error) {
	var opt *CallOption
	if len(opts) != 0 {
		opt = opts[0]
	} else {
		opt = defaultCallOption
	}
	// 构造请求消息
	var payload []byte
	if reqPb, ok := req.(proto.Message); ok {
		payload, err = c.opt.Marshaler.Marshal(reqPb)
		if err != nil {
			return
		}
	} else if reqData, ok := req.([]byte); ok {
		payload = reqData
	} else {
		err = ErrInvalidReqData
		return
	}
	seq := c.genMsgId.Add(1)
	rpcReq := &RpcRequestMessage{
		Seq:         seq,
		ServiceName: service,
		MethodName:  method,
		Md:          opt.ReqMd,
		Payload:     payload,
	}
	buffer, _err := c.encodeRpcReq(rpcReq)
	if _err != nil {
		err = _err
		return
	}
	if err = c.AsyncWrite(buffer); err != nil {
		return
	}

	// 处理异步
	if opt.Async {
		if ch := opt.AsyncRetChan; ch != nil {
			var expireAt int64
			if opt.Timeout > 0 {
				expireAt = time.Now().UnixMilli() + int64(opt.Timeout.Milliseconds())
				c.tmoutMtx.Lock()
				val, ok := c.timeouts.Get(expireAt)
				var set map[uint32]struct{}
				if ok {
					set = val.(map[uint32]struct{})
				} else {
					set = make(map[uint32]struct{}, 1)
					c.timeouts.Put(expireAt, set)
				}
				set[seq] = struct{}{}
				c.tmoutMtx.Unlock()
				//fmt.Printf("seq %d deadline %v\n", seq, expireAt)
				if !c.runTimeout.Load() {
					c.runTimeout.Store(true)
					go c.processTimeout()
				} else {
					c.updateTimerTrig <- expireAt
				}
			}
			callInfo := &callInfo{asyncRet: ch, outRsp: outRsp, expireAt: expireAt, passThrough: opt.PassThrough}
			c.mtx.Lock()
			c.waitRsps[seq] = callInfo
			c.mtx.Unlock()
		}
		return
	}

	// 处理同步
	syncRet := make(chan *callResult, 1)
	callInfo := &callInfo{syncRet: syncRet}
	c.mtx.Lock()
	c.waitRsps[seq] = callInfo
	c.mtx.Unlock()
	defer func() {
		close(syncRet)
		c.mtx.Lock()
		delete(c.waitRsps, seq)
		c.mtx.Unlock()
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
				return nil, nil, ret.senderr
			}
			if ret.rpcRsp == nil {
				// 发送成功，但是还没有结果, 也许还在返回的路上
				// TODO: 解决该问题
				return nil, nil, ErrWaitReplyExceed
			}
			return c.decodeRpcRsp(ret.rpcRsp, outRsp)
		default:
			return nil, nil, ctx.Err()
		}
	case ret := <-syncRet:
		if ret.senderr != nil {
			return nil, nil, ret.senderr
		}
		return c.decodeRpcRsp(ret.rpcRsp, outRsp)
	}
}

func (c *ClientConn) AsyncWrite(buffer *netpoll.LinkBuffer) error {
	if !c.conn.IsActive() {
		mlog.Warn("rpc client conn is not active, now reconnect")
		if err := c.reconnect(); err != nil {
			return err
		}
	}
	if err := c.conn.Writer().Append(buffer); err != nil {
		mlog.Error("rpc client conn Append err:%v", err)
		return err
	}
	if err := c.conn.Writer().Flush(); err != nil {
		mlog.Error("rpc client conn Flush err:%v", err)
		return err
	}
	return nil
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

func (c *ClientConn) decodeRpcRsp(rpcRsp *RpcResponseMessage, out proto.Message) (rspMd *Meta, rspData []byte, err error) {
	rspMd = rpcRsp.Md
	if err = rpcRsp.ParserError(); err != nil {
		return
	}
	if out != nil {
		if err = c.opt.Unmarshaler.Unmarshal(rpcRsp.Payload, out); err != nil {
			mlog.Error("rpc client failed to unmarshal response: %v", err)
			return
		}
	}
	rspData = rpcRsp.Payload
	return
}

func (cli *ClientConn) onRecvMsg(_ context.Context, conn netpoll.Connection) (err error) {
	reader := conn.Reader()
	mlog.Debug("%d client read buffer before size:%d", util.GoroutineID(), reader.Len())
	defer func() {
		mlog.Debug("%d client read buffer surplus size:%d", util.GoroutineID(), reader.Len())
	}()
	for {
		if reader.Len() <= msgLenSize {
			// Peek方法可能会阻塞
			return nil
		}
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
			mlog.Error("proto.Unmarshal RpcRequestMessage err:%v\n", err)
			return err
		}
		mlog.Debug("recv RpcResponseMessage msgId:%v", msg.Seq)

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

				rspMd, rspData, callErr := cli.decodeRpcRsp(msg, callInfo.outRsp)
				asyncRet := &AsyncCallResult{Err: callErr, RspMd: rspMd, PassThrough: callInfo.passThrough}
				if callInfo.outRsp != nil {
					asyncRet.Rsp = callInfo.outRsp
				} else {
					asyncRet.Rsp = rspData
				}
				select {
				case callInfo.asyncRet <- asyncRet:
					mlog.Debug("async call push result msgId:%v", msg.Seq)
				default:
					mlog.Error("async call result chan is full!!!")
					callInfo.asyncRet <- asyncRet
				}
			}
		} else {
			//rpc异步请求不需要response的情况
			//mlog.Debug("cli.waitRsps not find msgId:%v", msg.Seq)
		}
	}
}

func (c *ClientConn) processTimeout() {
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
		case <-c.quit:
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
					expireAt = newExpireAt
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
					callInfo.asyncRet <- &AsyncCallResult{Err: errors.New("rpc async call timeout"), PassThrough: callInfo.passThrough}
				}
			}
		}
	}
}

func (c *ClientConn) reconnect() error {
	conn, err := netpoll.DialConnection(c.network, c.address, c.opt.DailTimeout)
	if err != nil {
		mlog.Error("reconnect, rpc client Dial error %v", err)
		return err
	} else {
		if err := c.conn.Close(); err != nil {
			mlog.Error("reconnect, rpc client Close error %v", err)
		}
		c.conn = conn
		c.initConn(conn)
	}
	return nil
}

func (c *ClientConn) initConn(conn netpoll.Connection) {
	conn.SetOnRequest(func(ctx context.Context, nc netpoll.Connection) error {
		return c.onRecvMsg(ctx, nc)
	})
	if c.opt.OnClientClose != nil {
		conn.AddCloseCallback(c.opt.OnClientClose)
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

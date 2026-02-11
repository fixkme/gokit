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
	"google.golang.org/protobuf/proto"
)

type ClientConn struct {
	opt      *ClientOptions
	conn     netpoll.Connection
	wmtx     sync.Mutex // 写保护
	network  string
	address  string
	closed   atomic.Bool
	genMsgId atomic.Uint32
	waitRsps map[uint32]*callInfo
	mtx      sync.Mutex

	timeouts        *redblacktree.Tree // expireAt => set[msgId]
	tmoutMtx        sync.Mutex
	runTimeout      atomic.Bool
	quit            chan struct{}
	updateTimerTrig chan int64
}

func NewClientConn(network, address string, opt *ClientOptions) (*ClientConn, error) {
	opt.setDefault()
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

func (opt *ClientOptions) setDefault() {
	if opt.DailTimeout == 0 {
		opt.DailTimeout = 3 * time.Second
	}
	if opt.MsgUnmarshaler == nil {
		opt.MsgUnmarshaler = proto.UnmarshalOptions{}
	}
	if opt.MsgMarshaler == nil {
		opt.MsgMarshaler = proto.MarshalOptions{}
	}
}

var (
	defaultCallOption  = &CallOption{}
	ErrInvalidReqData  = errors.New("invalid request data")
	ErrWaitReplyExceed = errors.New("wait reply exceed")
	ErrRpcCallTimeout  = errors.New("rpc call timeout")
	ErrWriteClosed     = errors.New("write: client closed")
)

func (c *ClientConn) Close() error {
	c.wmtx.Lock()
	defer c.wmtx.Unlock()
	// close fd 也是一种写操作，需要锁保护
	if c.closed.Swap(true) {
		return nil
	}
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
	switch reqVal := req.(type) {
	case []byte:
		payload = reqVal
	case proto.Message:
		payload, err = c.opt.MsgMarshaler.Marshal(reqVal)
		if err != nil {
			return
		}
	default:
		err = ErrInvalidReqData
		return
	}

	md := opt.ReqMd
	if opt.Async && opt.AsyncRetChan == nil {
		if md == nil {
			md = &Meta{}
		}
		md.SetInt(RpcMsgTagKey, RpcMsgTag_AsyncNoResp)
	}
	seq := c.genMsgId.Add(1)
	rpcReq := RpcRequestMessage{
		Seq:         seq,
		ServiceName: service,
		MethodName:  method,
		Md:          md,
		Payload:     payload,
	}
	buffer, _err := c.encodeRpcReq(&rpcReq)
	if err = _err; _err != nil {
		return
	}
	if err = c.syncWrite(buffer); err != nil {
		return
	}

	// 处理异步
	if opt.Async {
		err = c.asyncCallWait(seq, outRsp, opt)
		return
	}

	// 处理同步
	return c.syncCallWait(ctx, seq, outRsp, opt)
}

func (c *ClientConn) asyncCallWait(seq uint32, outRsp proto.Message, opt *CallOption) error {
	ch := opt.AsyncRetChan
	if ch == nil {
		return nil
	}
	var expireAt int64
	if opt.Timeout > 0 {
		expireAt = time.Now().UnixMilli() + int64(opt.Timeout.Milliseconds())
	}
	callInfo := &callInfo{asyncRet: ch, outRsp: outRsp, expireAt: expireAt, passThrough: opt.PassThrough}
	c.mtx.Lock()
	c.waitRsps[seq] = callInfo
	c.mtx.Unlock()

	if expireAt > 0 {
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
		if !c.runTimeout.Swap(true) {
			go c.processTimeout()
		} else {
			// 尝试唤醒更新timer, 失败表示已经有唤醒信号了
			select {
			case c.updateTimerTrig <- expireAt:
			default:
			}
		}
	}
	return nil
}

func (c *ClientConn) syncCallWait(ctx context.Context, seq uint32, outRsp proto.Message, opt *CallOption) (rspMd *Meta, rspData []byte, err error) {
	syncRet := make(chan *callResult, 1)
	callInfo := &callInfo{syncRet: syncRet}
	c.mtx.Lock()
	c.waitRsps[seq] = callInfo
	c.mtx.Unlock()
	defer func() {
		c.mtx.Lock()
		delete(c.waitRsps, seq)
		c.mtx.Unlock()
		// 必须先删除掉，再关闭，否则写入closed chan会panic
		close(syncRet)
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
			return c.decodeRpcRsp(ret.rpcRsp, outRsp)
		default:
			if err = ctx.Err(); errors.Is(err, context.DeadlineExceeded) {
				// 发送成功，但是还没有结果, 也许还在返回的路上, 业务层处理，见README.md
				return nil, nil, ErrWaitReplyExceed
			}
			return nil, nil, ctx.Err()
		}
	case ret := <-syncRet:
		if ret.senderr != nil {
			return nil, nil, ret.senderr
		}
		return c.decodeRpcRsp(ret.rpcRsp, outRsp)
	}
}

func (c *ClientConn) syncWrite(buffer *netpoll.LinkBuffer) error {
	c.wmtx.Lock()
	defer c.wmtx.Unlock()

	if c.closed.Load() {
		return ErrWriteClosed
	}
	// 重连也是一种写操作，需要锁保护
	if !c.conn.IsActive() {
		mlog.Warn("rpc client conn is not active when asyncWrite, now reconnect")
		c.conn.Close()
		if err := c.reconnect(); err != nil {
			return err
		}
	}
	if err := c.conn.Writer().Append(buffer); err != nil {
		mlog.Errorf("rpc client conn Append err:%v", err)
		return err
	}
	if err := c.conn.Writer().Flush(); err != nil {
		mlog.Errorf("rpc client conn Flush err:%v", err)
		return err
	}
	return nil
}

func (c *ClientConn) encodeRpcReq(rpcReq *RpcRequestMessage) (*netpoll.LinkBuffer, error) {
	sz := rpcMsgMarshaler.Size(rpcReq)
	buffer := netpoll.NewLinkBuffer(msgLenSize + sz)
	// 获取整个写入空间(长度头+消息体)
	buf, err := buffer.Malloc(msgLenSize + sz)
	if err != nil {
		return nil, err
	}
	data, err := rpcMsgMarshaler.MarshalAppend(buf[msgLenSize:msgLenSize], rpcReq)
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
		if err = c.opt.MsgUnmarshaler.Unmarshal(rpcRsp.Payload, out); err != nil {
			mlog.Errorf("rpc client failed to unmarshal response: %v", err)
			return
		}
	}
	rspData = rpcRsp.Payload
	return
}

func (cli *ClientConn) onRecvMsg(_ context.Context, conn netpoll.Connection) (err error) {
	reader := conn.Reader()
	lenBuf, _err := reader.Next(msgLenSize)
	if _err != nil {
		return _err
	}
	dataLen := int(byteOrder.Uint32(lenBuf))
	packetBuf, _err := reader.Next(dataLen)
	if _err != nil {
		return _err
	}
	// 反序列化
	msg := &RpcResponseMessage{}
	if err = rpcMsgUnmarshaler.Unmarshal(packetBuf, msg); err != nil {
		mlog.Errorf("proto.Unmarshal RpcResponseMessage err:%v\n", err)
		return err
	}
	mlog.Debugf("recv RpcResponseMessage msgId:%v", msg.Seq)

	// TODO panic cli.mtx.Unlock()
	cli.mtx.Lock()
	callInfo, ok := cli.waitRsps[msg.Seq]
	if ok {
		if callInfo.syncRet != nil {
			callInfo.syncRet <- &callResult{rpcRsp: msg}
			cli.mtx.Unlock()
		} else if callInfo.asyncRet != nil {
			delete(cli.waitRsps, msg.Seq)
			cli.mtx.Unlock()

			if exAt := callInfo.expireAt; exAt > 0 {
				cli.tmoutMtx.Lock()
				val, ok := cli.timeouts.Get(exAt)
				if ok {
					set := val.(map[uint32]struct{})
					delete(set, msg.Seq)
				}
				cli.tmoutMtx.Unlock()
			}

			rspMd, rspData, callErr := cli.decodeRpcRsp(msg, callInfo.outRsp)
			asyncRet := &AsyncCallResult{Err: callErr, RspMd: rspMd, PassThrough: callInfo.passThrough}
			if callInfo.outRsp != nil {
				asyncRet.Rsp = callInfo.outRsp
			} else {
				asyncRet.Rsp = rspData
			}
			select {
			case callInfo.asyncRet <- asyncRet:
				mlog.Debugf("async call push result msgId:%v", msg.Seq)
			default:
				mlog.Warn("async call result chan is full!!!")
				callInfo.asyncRet <- asyncRet
			}
		}
	} else {
		cli.mtx.Unlock()
		// 不在 cli.waitRsps 的情况
		// 1、rpc异步请求不需要response
		// 2、rpc同步请求过期删除，异步请求需要response，过期导致在timeout协程删除。业务层处理，见README.md
		mlog.Warnf("cli.waitRsps not find msgId:%d", msg.Seq)
	}
	return
}

func (c *ClientConn) processTimeout() {
	nowMs := time.Now().UnixMilli()
	c.tmoutMtx.Lock()
	expireAt := c.timeouts.Left().Key.(int64)
	c.tmoutMtx.Unlock()
	dur := expireAt - nowMs
	timer := time.NewTimer(time.Millisecond * time.Duration(dur))
	defer timer.Stop()
	var expireFirst int64
	for {
		select {
		case <-c.quit:
			return
		case <-c.updateTimerTrig:
			c.tmoutMtx.Lock()
			expireFirst = c.timeouts.Left().Key.(int64)
			c.tmoutMtx.Unlock()
			if expireFirst < expireAt {
				expireAt = expireFirst
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
			nowMs = time.Now().UnixMilli()
			if c.timeouts.Size() > 0 {
				expireAt = c.timeouts.Left().Key.(int64)
				dur = expireAt - nowMs
				timer.Reset(time.Millisecond * time.Duration(dur))
			} else {
				defaultDelay := time.Minute * 2
				expireAt = nowMs + defaultDelay.Milliseconds()
				timer.Reset(defaultDelay)
			}
			c.tmoutMtx.Unlock()
			for _, callInfo := range callInfos {
				if callInfo.asyncRet != nil {
					callInfo.asyncRet <- &AsyncCallResult{Err: ErrRpcCallTimeout, PassThrough: callInfo.passThrough}
				}
			}
		}
	}
}

func (c *ClientConn) reconnect() error {
	conn, err := netpoll.DialConnection(c.network, c.address, c.opt.DailTimeout)
	if err != nil {
		mlog.Errorf("reconnect, rpc client Dial error %v", err)
		return err
	}
	if err := c.conn.Close(); err != nil {
		mlog.Errorf("reconnect, rpc client Close old conn error %v", err)
	}
	c.initConn(conn)
	return nil
}

func (cc *ClientConn) initConn(conn netpoll.Connection) {
	cc.conn = conn
	conn.SetReadTimeout(cc.opt.ReadTimeout)
	conn.SetWriteTimeout(cc.opt.WriteTimeout)
	conn.SetOnRequest(func(ctx context.Context, nc netpoll.Connection) error {
		return cc.onRecvMsg(ctx, nc)
	})
	if cc.opt.OnClientClose != nil {
		conn.AddCloseCallback(cc.opt.OnClientClose)
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

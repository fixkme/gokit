package rpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	sync "sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"
	"github.com/cloudwego/netpoll/mux"
	"github.com/fixkme/gokit/util/errs"
	"google.golang.org/protobuf/proto"
)

type ClientConn struct {
	conn     netpoll.Connection
	wqueue   *mux.ShardQueue
	genMsgId atomic.Uint32
	waitRsps map[uint32]chan *callResult
	mtx      sync.Mutex
}

func NewClientConn(network, address string, timeout time.Duration) (*ClientConn, error) {
	conn, err := netpoll.DialConnection(network, address, timeout)
	if err != nil {
		return nil, err
	}
	cliConn := &ClientConn{
		wqueue:   mux.NewShardQueue(128, conn),
		waitRsps: make(map[uint32]chan *callResult),
	}
	conn.SetOnRequest(func(ctx context.Context, connection netpoll.Connection) error {
		return onClientMsg(ctx, connection, cliConn)
	})
	conn.AddCloseCallback(onClientClose)
	cliConn.conn = conn
	return cliConn, nil
}

func (cs *ClientConn) Invoke(ctx context.Context, path string, req, rsp proto.Message, opts ...*CallOption) error {
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
		Md:          opt.Md,
		Payload:     payload,
	}
	buffer, err := cs.encodeRpcReq(rpcReq)
	if err != nil {
		return err
	}
	cs.AsyncWrite(buffer)
	if !opt.Sync {
		return nil
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

func (c *ClientConn) decodeRpcRsp(rpcRsp *RpcResponseMessage, out proto.Message) error {
	if rpcRsp.Error != "" {
		if rpcRsp.Ecode != 0 {
			return errs.CreateCodeError(rpcRsp.Ecode, rpcRsp.Error)
		}
		return errors.New(rpcRsp.Error)
	}
	if out != nil {
		if err := proto.Unmarshal(rpcRsp.Payload, out); err != nil {
			log.Printf("failed to unmarshal response: %v", err)
			return err
		}
	}
	return nil
}

func onClientClose(conn netpoll.Connection) error {
	log.Println("client is Closed")
	return nil
}

func onClientMsg(ctx context.Context, conn netpoll.Connection, cli *ClientConn) (err error) {
	reader := conn.Reader()
	log.Printf("%d client read buffer surplus size:%d", GoroutineID(), reader.Len())
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
			log.Printf("proto.Unmarshal RpcRequestMessage err:%v\n", err)
			return err
		}

		cli.mtx.Lock()
		rch, ok := cli.waitRsps[msg.Seq]
		cli.mtx.Unlock()
		if ok {
			rch <- &callResult{rpcRsp: msg}
		}
	}
}

func GoroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false) // 获取当前 goroutine 的调用栈
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id := 0
	fmt.Sscanf(idField, "%d", &id)
	return id
}

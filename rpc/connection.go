package rpc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	sync "sync"
	"sync/atomic"
	"time"

	"github.com/fixkme/gokit/util/errs"
	"google.golang.org/protobuf/proto"
)

type Connection struct {
	*net.TCPConn
	genMsgId atomic.Uint32
	waitRsps map[uint32]chan *callResult
	mtx      sync.Mutex
	sendCh   chan *RpcRequestMessage
	recvCh   chan *RpcResponseMessage
	quit     chan struct{}
}

type callResult struct {
	sendok  bool
	senderr error
	rpcRsp  *RpcResponseMessage
}

func NewConnection(addr string) (c *Connection, err error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	c = &Connection{
		TCPConn:  conn.(*net.TCPConn),
		waitRsps: make(map[uint32]chan *callResult),
		sendCh:   make(chan *RpcRequestMessage, 1024),
		recvCh:   make(chan *RpcResponseMessage, 1024),
		quit:     make(chan struct{}, 1),
	}
	go c.sendCoroutine(c.quit)
	go c.readCoroutine()
	return
}

func (c *Connection) Invoke(ctx context.Context, path string, req, rsp proto.Message, opts ...*CallOption) error {
	var opt *CallOption
	if len(opts) != 0 {
		opt = opts[0]
	} else {
		opt = &CallOption{}
	}
	seq, err := c.sendRpcRequest(ctx, path, req, opt.Md)
	if err != nil {
		return err
	}
	if opt.Sync {
		retCh := make(chan *callResult, 1)
		c.mtx.Lock()
		c.waitRsps[seq] = retCh
		c.mtx.Unlock()

		defer func() {
			c.mtx.Lock()
			delete(c.waitRsps, seq)
			c.mtx.Unlock()
		}()

		select {
		case <-ctx.Done():
			select {
			case ret := <-retCh:
				if ret.senderr != nil {
					return ret.senderr
				}
				if ret.rpcRsp == nil {
					// 发送成功，但是还没有结果
				}
			default:
				return ctx.Err()
			}
		case ret := <-retCh:
			if ret.senderr != nil {
				return ret.senderr
			}
			v := ret.rpcRsp
			if v.Error != "" {
				if v.Ecode != 0 {
					return errs.CreateCodeError(v.Ecode, v.Error)
				}
				return errors.New(v.Error)
			}
			if rsp != nil {
				if err := proto.Unmarshal(v.Payload, rsp); err != nil {
					log.Printf("failed to unmarshal response: %v", err)
					return err
				}
			}
			return nil
		}
	}
	return nil
}

func (c *Connection) sendRpcRequest(ctx context.Context, path string, data proto.Message, md *Meta) (uint32, error) {
	// 构造请求消息
	v2 := strings.SplitN(path, "/", 2)
	if len(v2) != 2 {
		return 0, fmt.Errorf("invalid path: %s", path)
	}
	payload, err := proto.Marshal(data)
	if err != nil {
		return 0, err
	}

	seq := c.genMsgId.Add(1)
	rpcReq := &RpcRequestMessage{
		Seq:         seq,
		ServiceName: v2[0],
		MethodName:  v2[1],
		Md:          md,
		Payload:     payload,
	}

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case c.sendCh <- rpcReq:
			return seq, nil
		}
	}
}

func (c *Connection) sendCoroutine(quit <-chan struct{}) {
	for {
		select {
		case <-quit:
			return
		case msg, ok := <-c.sendCh:
			if ok {
				c.serializeRpcRequest(msg)
			}
		}
	}
}

func (c *Connection) serializeRpcRequest(msg *RpcRequestMessage) (err error) {
	defer func() {
		c.mtx.Lock()
		retCh := c.waitRsps[msg.Seq]
		c.mtx.Unlock()
		if retCh != nil {
			retCh <- &callResult{senderr: err}
		} else if err != nil {
			log.Printf("send rpc req failed, err:%v, msg:%v,%s:%s\n", err, msg.Seq, msg.ServiceName, msg.MethodName)
		}
	}()
	// 序列化请求消息
	buf, err1 := proto.Marshal(msg)
	if err1 != nil {
		log.Printf("Failed to marshal request: %v\n", err1)
		err = err1
		return
	}
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(len(buf)))
	if _, err = c.Write(lenBuf); err != nil {
		log.Printf("Failed to Write lenBuf: %v\n", err)
	}
	if _, err = c.Write(buf); err != nil {
		log.Printf("Failed to Write buf: %v\n", err)
	}
	log.Printf("send rpc req succeed:%v\n", msg)
	return
}

func (c *Connection) readCoroutine() {
	for {
		lenBuf := make([]byte, 4)
		_, err := c.Read(lenBuf)
		if err != nil {
			return
		}
		dataLen := binary.LittleEndian.Uint32(lenBuf)
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
		fmt.Printf("recv rpc rsp:%v\n", msg)

		c.mtx.Lock()
		retCh, ok := c.waitRsps[msg.Seq]
		c.mtx.Unlock()
		if ok {
			retCh <- &callResult{rpcRsp: msg}
		}
	}
}

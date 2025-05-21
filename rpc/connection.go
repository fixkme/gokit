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

	"google.golang.org/protobuf/proto"
)

type Connection struct {
	*net.TCPConn
	genMsgId atomic.Uint32
	waitRsps map[uint32]*callInfo
	mtx      sync.Mutex
	sendCh   chan *RpcRequestMessage
	recvCh   chan *RpcResponseMessage
	quit     chan struct{}
}

type callInfo struct {
	ch chan *RpcResponseMessage
}

func NewConnection(addr string) (c *Connection, err error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	c = &Connection{
		TCPConn:  conn.(*net.TCPConn),
		waitRsps: make(map[uint32]*callInfo),
		sendCh:   make(chan *RpcRequestMessage, 1024),
		recvCh:   make(chan *RpcResponseMessage, 1024),
		quit:     make(chan struct{}, 1),
	}
	go c.sendCoroutine(c.quit)
	go c.readCoroutine()
	return
}

func (c *Connection) Invoke(ctx context.Context, path string, req, rsp proto.Message, sync bool) error {
	seq, err := c.sendRpcRequest(ctx, path, req)
	if err != nil {
		return err
	}
	if sync {
		ch := make(chan *RpcResponseMessage, 1)
		c.mtx.Lock()
		c.waitRsps[seq] = &callInfo{ch: ch}
		c.mtx.Unlock()

		defer func() {
			c.mtx.Lock()
			delete(c.waitRsps, seq)
			c.mtx.Unlock()
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case v := <-ch:
			if v.Error != "" {
				return errors.New(v.Error)
			}
			if err := proto.Unmarshal(v.Payload, rsp); err != nil {
				log.Printf("failed to unmarshal response: %v", err)
				return err
			}
			return nil
		}
	}
	return nil
}

func (c *Connection) sendRpcRequest(ctx context.Context, path string, data proto.Message) (uint32, error) {
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
				// 序列化请求消息
				buf, err := proto.Marshal(msg)
				if err != nil {
					log.Printf("Failed to marshal request: %v\n", err)
					continue
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
			}
		}
	}
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
		cb, ok := c.waitRsps[msg.Seq]
		c.mtx.Unlock()
		if ok {
			cb.ch <- msg
		}
	}
}

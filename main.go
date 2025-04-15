package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/fixkme/gokit/rpc"
	"google.golang.org/protobuf/proto"
)

type RpcClient struct {
	conn *net.TCPConn
}

func (c *RpcClient) call(path string, data proto.Message) {
	// 构造请求消息
	v2 := strings.SplitN(path, "/", 2)
	if len(v2) != 2 {
		log.Fatalf("invalid path: %s", path)
	}
	payload, err := proto.Marshal(data)
	if err != nil {
		log.Fatalf("failed to marshal request: %v", err)
	}
	rpcReq := &rpc.RpcRequestMessage{
		ServiceName: v2[0],
		MethodName:  v2[1],
		Payload:     payload,
	}
	// 序列化请求消息
	buf, err := proto.Marshal(rpcReq)
	if err != nil {
		log.Printf("Failed to marshal request: %v\n", err)
		return
	}
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(buf)))
	if _, err = c.conn.Write(lenBuf); err != nil {
		log.Printf("Failed to Write lenBuf: %v\n", err)
		return
	}
	if _, err = c.conn.Write(buf); err != nil {
		log.Printf("Failed to Write buf: %v\n", err)
		return
	}
	fmt.Println("call succeed")
}
func (c *RpcClient) connect() {
	// 连接到服务器
	conn, err := net.DialTimeout("tcp4", "127.0.0.1:2333", 5*time.Second)
	if err != nil {
		log.Printf("Failed to dial: %v", err)
		return
	}
	c.conn = conn.(*net.TCPConn)
	fmt.Println("connect succeed")
}

func (c *RpcClient) test() {
	req := &rpc.RpcRequestMessage{}
	c.call("Game/GameUpdateActivityStatus", req)
}

func (c *RpcClient) read() {
	for {
		lenBuf := make([]byte, 4)
		_, err := c.conn.Read(lenBuf)
		if err != nil {
			return
		}
		dataLen := binary.BigEndian.Uint32(lenBuf)
		msgBuf := make([]byte, dataLen)
		_, err = c.conn.Read(msgBuf)
		if err != nil {
			return
		}
		// 反序列化
		msg := &rpc.RpcResponseMessage{}
		if err = proto.Unmarshal(msgBuf, msg); err != nil {
			return
		}

		fmt.Printf("%v\n", msg)
		return
	}
}

func main() {
	c := &RpcClient{}
	c.connect()

	go c.read()

	fmt.Println("start ...")
	c.test()

	select {}
}

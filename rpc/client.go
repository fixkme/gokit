package rpc

import (
	"encoding/binary"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

type Client struct {
}

type ClientHander struct {
}

func (h *ClientHander) OnBoot(eng gnet.Engine) (action gnet.Action) {
	log.Println("OnBoot")
	return 0
}

// OnShutdown fires when the engine is being shut down, it is called right after
// all event-loops and connections are closed.
func (h *ClientHander) OnShutdown(eng gnet.Engine) {
	log.Println("OnShutdown")
}

// OnOpen fires when a new connection has been opened.
//
// The Conn c has information about the connection such as its local and remote addresses.
// The parameter out is the return value which is going to be sent back to the remote.
// Sending large amounts of data back to the remote in OnOpen is usually not recommended.
func (h *ClientHander) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	log.Println("OnOpen")
	return nil, gnet.None
}

// OnClose fires when a connection has been closed.
// The parameter err is the last known connection error.
func (h *ClientHander) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	log.Println("OnClose")
	return 0
}

// OnTraffic fires when a socket receives data from the remote.
//
// Note that the []byte returned from Conn.Peek(int)/Conn.Next(int) is not allowed to be passed to a new goroutine,
// as this []byte will be reused within event-loop after OnTraffic() returns.
// If you have to use this []byte in a new goroutine, you should either make a copy of it or call Conn.Read([]byte)
// to read data into your own []byte, then pass the new []byte to the new goroutine.
func (h *ClientHander) OnTraffic(c gnet.Conn) (action gnet.Action) {
	log.Println("OnTraffic")
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

		fmt.Printf("rpc rsp:%v\n", msg)
	}
}

// OnTick fires immediately after the engine starts and will fire again
// following the duration specified by the delay return value.
func (h *ClientHander) OnTick() (delay time.Duration, action gnet.Action) {
	log.Println("OnTick")
	return 5 * time.Second, 0
}

func GoroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false) // 获取当前 goroutine 的调用栈
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id := 0
	fmt.Sscanf(idField, "%d", &id)
	return id
}

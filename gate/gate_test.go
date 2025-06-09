package gate

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestServer(t *testing.T) {
	opt := &ServerOptions{
		Addr:             "tcp://127.0.0.1:2333",
		HandshakeTimeout: time.Second,
	}
	quit := make(chan struct{})
	server := NewServer(opt, NewRouterPool(4, 100, quit))
	server.Run()
}

func TestWsClient(t *testing.T) {
	cliNum := 20
	for i := 0; i < cliNum; i++ {
		go client(i)
	}

	time.Sleep(time.Second * 10)
}

func client(id int) {
	conn, err := net.Dial("tcp", "127.0.0.1:2333")
	if err != nil {
		panic(err)
	}
	key, _ := generateWebSocketKey()
	content := "GET /chat HTTP/1.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + key + "\r\n" +
		"x-player-id: " + strconv.Itoa(id) + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err = conn.Write([]byte(content)); err != nil {
		panic(err)
	}
	//fmt.Printf("发送握手请求报文完毕\n")
	fmt.Println(string(readHttpReply(conn)))

	n := 5
	for i := 0; i < n; i++ {
		pd := []byte(fmt.Sprintf("%d hello world %d", id, i+1))
		wsh := &WsHead{Fin: true, OpCode: OpBinary, Masked: true, Length: int64(len(pd))}
		geneMask(&wsh.Mask, pd)
		hbf, _ := MakeWsHeadBuff(wsh)
		//fmt.Println(wsh.Mask, ",", string(pd))
		if _, err = conn.Write(hbf); err != nil {
			fmt.Println(err)
			break
		}
		if _, err = conn.Write(pd); err != nil {
			fmt.Println(err)
			break
		}
		fmt.Println(id, "echo:", string(readWsReply(conn)))
		time.Sleep(time.Millisecond * 100)
	}
}

// 客户端生成 WebSocket key
func generateWebSocketKey() (string, error) {
	// 生成 16 字节的随机数
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	// 进行 Base64 编码
	return base64.StdEncoding.EncodeToString(key), nil
}

func geneMask(mask *[4]byte, payload []byte) {
	k := (*mask)[:]
	_, err := rand.Read(k)
	if err != nil {
		panic(err)
	}
	MaskWsPayload(*mask, payload)
}

func readHttpReply(c net.Conn) []byte {
	rpy := make([]byte, 1024)
	n, err := c.Read(rpy)
	if err != nil {
		panic(err)
	}
	return rpy[:n]
}

func readWsReply(c net.Conn) []byte {
	wsh, err := ReadWsHeader(c)
	if err != nil {
		panic(err)
	}
	payload := make([]byte, wsh.Length)
	n, err := io.ReadFull(c, payload)
	if err != nil {
		panic(err)
	}
	_ = n
	//fmt.Println("收到字节长度", n)
	return payload
}

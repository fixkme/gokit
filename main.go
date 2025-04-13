package main

import (
	"log"

	"github.com/panjf2000/gnet/v2"
)

type echoServer struct {
	gnet.BuiltinEventEngine
	gnet.Engine
	addr      string
	multicore bool
}

func (es *echoServer) OnTraffic(c gnet.Conn) gnet.Action {
	buf, _ := c.Next(-1) // 读取所有数据
	c.Write(buf)         // 回写数据（Echo）
	return gnet.None
}

func main() {
	server := &echoServer{
		addr:      "tcp://:8080",
		multicore: true, // 多核模式
	}

	log.Printf("Server is listening on %s\n", server.addr)
	if err := gnet.Run(server, server.addr, gnet.WithMulticore(server.multicore)); err != nil {
		log.Fatalf("Server exited with error: %v", err)
	}
}

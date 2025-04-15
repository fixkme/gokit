package rpc

import (
	"log"
	"testing"
)

func TestServer(t *testing.T) {
	opt := &ServerOpt{
		Addr:          "tcp4://127.0.0.1:2333",
		ProcessorSize: 4,
	}
	server := NewServer(opt)

	log.Printf("Server is listening on %s\n", opt.Addr)
	server.Run()

}

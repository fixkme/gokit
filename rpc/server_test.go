package rpc

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/fixkme/protoc-gen-gom/example/pbout/go/gate"
)

func TestServer(t *testing.T) {
	opt := &ServerOpt{
		Addr:          "tcp4://127.0.0.1:2333",
		ProcessorSize: 4,
	}
	server := NewServer(opt)
	gate.RegisterGateServer(server, &ServiceImp{})

	log.Printf("Server is listening on %s\n", opt.Addr)
	server.Run()

}

type ServiceImp struct {
}

func (s *ServiceImp) NoticePlayer(context.Context, *gate.CNoticePlayer) (*gate.SNoticePlayer, error) {
	//return &gate.SNoticePlayer{}, nil
	return nil, fmt.Errorf("handler NoticePlayer logic error")
}

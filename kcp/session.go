package kcp

import (
	"context"
	"sync"
	"time"

	"github.com/fixkme/gokit/util"
)

type Session struct {
	remoteAddr string
	kcp        *KCP
	cli        ClientInterface
	opt        *ServerOptions
	server     *Server
	inputCh    chan []byte
	outputCh   chan []byte
	ctx        context.Context
	cancel     context.CancelFunc
}

func newSession(convId uint32, raddr string, s *Server) *Session {
	opt := s.opt
	b2i := util.BoolTo[int]
	sess := &Session{
		remoteAddr: raddr,
		opt:        opt,
		server:     s,
		inputCh:    make(chan []byte, 1024),
		outputCh:   make(chan []byte, 1024),
	}
	sess.kcp = NewKCP(convId, sess.kcpOutput)

	sess.kcp.SetMtu(opt.KcpMtu)
	sess.kcp.WndSize(opt.KcpSendWnd, opt.KcpRecvWnd)
	sess.kcp.NoDelay(b2i(opt.KcpNoDelay), opt.KcpUpdateInterval, opt.KcpFastResend, b2i(opt.KcpNoCwnd))
	if opt.KcpMinRto > 0 {
		sess.kcp.rx_minrto = uint32(opt.KcpMinRto)
	}
	if opt.KcpStreamMode {
		sess.kcp.stream = 1
	}
	sess.ctx, sess.cancel = context.WithCancel(s.ctx)
	go sess.kcpLoop(s.wg)
	return sess
}

func (s *Session) kcpLoop(wg *sync.WaitGroup) {
	defer wg.Done()

	interval := s.opt.KcpUpdateInterval
	updateTimer := time.NewTimer(time.Millisecond * time.Duration(interval))

	for {
		select {
		case <-s.ctx.Done():
			s.kcp.flush(IKCP_FLUSH_FULL)
			return
		case data := <-s.inputCh:
			s.onRead(data)
		case data := <-s.outputCh:
			s.kcp.Send(data)
			s.kcp.flush(IKCP_FLUSH_FULL)
		case <-updateTimer.C:
			s.kcp.Update()
			updateTimer.Reset(time.Millisecond * time.Duration(interval))
		}
	}
}

func (s *Session) onRead(data []byte) {
	s.kcp.Input(data, IKCP_PACKET_REGULAR, s.opt.ACKNoDelay)

	for i := 0; i < 10; i++ {
		msgLen := s.kcp.PeekSize()
		if msgLen < 0 {
			return
		}
		buff := make([]byte, msgLen)
		s.kcp.Recv(buff)
		if !s.cli.OnRead(buff) {
			break
		}
	}
}

func (s *Session) kcpOutput(data []byte, size int) {
	s.server.writeChan <- data
}

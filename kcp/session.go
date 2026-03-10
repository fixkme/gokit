package kcp

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fixkme/gokit/mlog"
	"github.com/fixkme/gokit/util"
)

type Session struct {
	convId      uint32
	remoteAddr  net.Addr
	kcp         *KCP
	sessHandler SessionHandler
	opt         *ServerOptions
	server      *Server
	inputCh     chan *inputData
	outputCh    chan []byte
	ctx         context.Context
	cancel      context.CancelFunc
	closed      atomic.Bool
}

func newSession(convId uint32, raddr net.Addr, sh SessionHandler, s *Server) *Session {
	opt := s.opt
	b2i := util.BoolTo[int]
	sess := &Session{
		convId:      convId,
		remoteAddr:  raddr,
		sessHandler: sh,
		opt:         opt,
		server:      s,
		inputCh:     make(chan *inputData, 1024),
		outputCh:    make(chan []byte, 1024),
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
	s.wg.Add(1)
	go sess.kcpLoop(s.wg)
	return sess
}

type inputData struct {
	data  []byte
	raddr net.Addr
}

func (s *Session) kcpLoop(wg *sync.WaitGroup) {
	defer wg.Done()

	interval := s.opt.KcpUpdateInterval
	updateTimer := time.NewTimer(time.Millisecond * time.Duration(interval))
	defer updateTimer.Stop()

	for {
		select {
		case <-s.ctx.Done():
			s.kcp.flush(IKCP_FLUSH_FULL)
			return
		case indata := <-s.inputCh:
			s.remoteAddr = indata.raddr // update remote addr
			s.onRead(indata.data)
		case data := <-s.outputCh:
			s.kcp.Send(data)
			s.kcp.flush(IKCP_FLUSH_FULL)
			if s.checkDeadLink() {
				return
			}
		case <-updateTimer.C:
			s.kcp.Update()
			if !s.checkDeadLink() {
				updateTimer.Reset(time.Millisecond * time.Duration(interval))
			} else {
				return
			}
		}
	}
}

func (s *Session) onRead(data []byte) {
	defer defaultBufferPool.Put(data)

	ok := s.kcp.Input(data, IKCP_PACKET_REGULAR, s.opt.ACKNoDelay)
	if ok < 0 {
		mlog.Debugf("session kcp input not ok %d", ok)
		return
	}

	data = data[:cap(data)]
	var buff []byte
	const maxProcess = 10
	for i := 0; i < maxProcess; i++ {
		msgLen := s.kcp.PeekSize()
		if msgLen < 0 {
			return
		}

		if msgLen > cap(data) {
			buff = make([]byte, msgLen)
		} else {
			buff = data[:msgLen]
		}
		s.kcp.Recv(buff)
		if !s.sessHandler.OnRead(buff) {
			break
		}
	}
}

type outputData struct {
	data  []byte
	raddr net.Addr
}

func (s *Session) kcpOutput(data []byte, size int) {
	buf := make([]byte, size)
	copy(buf, data[:size])
	select {
	case s.server.writeChan <- &outputData{data: buf, raddr: s.remoteAddr}:
	default:
		// kcp 会超时重发
		mlog.Debug("server write chan is full")
	}
}

func (s *Session) Send(data []byte) error {
	if len(data) == 0 {
		return errors.New("data is empty")
	}
	if s.checkDeadLink() {
		return errors.New("dead link")
	}
	count := (len(data) + int(s.kcp.mss) - 1) / int(s.kcp.mss)
	if count >= int(s.kcp.snd_wnd) {
		return errors.New("data is too large")
	}
	if s.kcp.WaitSnd() >= int(s.kcp.snd_wnd) {
		return errors.New("EAGAIN")
	}

	select {
	case s.outputCh <- data:
	default:
		return errors.New("EAGAIN")
	}
	return nil
}

func (s *Session) checkDeadLink() bool {
	deadLink := s.kcp.state > 0
	if deadLink && !s.closed.Swap(true) {
		s.sessHandler.OnClose()
		s.cancel()
		s.server.closeSession(s)
		return true
	}
	return deadLink
}

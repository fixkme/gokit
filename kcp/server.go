package kcp

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/fixkme/gokit/mlog"
)

type Server struct {
	opt       *ServerOptions
	conn      net.PacketConn
	acceptCh  chan *handshakeTask
	writeChan chan *outputData
	sessions  map[uint32]*Session
	sessMtx   sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        *sync.WaitGroup
}

func NewServer(laddr string, opt *ServerOptions) (*Server, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", laddr)
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		opt:       opt,
		conn:      conn,
		acceptCh:  make(chan *handshakeTask, 1024),
		writeChan: make(chan *outputData, 1024),
		sessions:  make(map[uint32]*Session),
		wg:        &sync.WaitGroup{},
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

func (s *Server) Run() error {
	// TODO
	go s.readLoop()
	go s.writeLoop()
	s.acceptLoop()
	return nil
}

func (s *Server) Close() error {
	// TODO
	s.cancel()
	s.wg.Wait()
	return nil
}

func (s *Server) readLoop() {
	buf := make([]byte, mtuLimit)
	for {
		n, from, err := s.conn.ReadFrom(buf)
		if err != nil {
			mlog.Errorf("server ReadFrom err:%v", err)
			if errors.Is(err, net.ErrClosed) {
				s.cancel()
				return
			}
			//s.notifyReadError(err)
			continue
		}
		mlog.Tracef("server ReadFrom ", from.String())
		s.onRead(buf[:n], from)
	}
}

func (s *Server) onRead(udpPacket []byte, from net.Addr) {
	if len(udpPacket) == 0 {
		return
	}

	cid, buf := s.opt.ParseConvId(udpPacket)
	if len(buf) == 0 {
		return
	}
	data := defaultBufferPool.Get()[:len(buf)]
	copy(data, buf)

	s.sessMtx.Lock()
	sess, exist := s.sessions[cid]
	s.sessMtx.Unlock()
	if exist && cid != 0 {
		select {
		case sess.inputCh <- &inputData{data: data, raddr: from}:
		default:
			// 丢弃
			defaultBufferPool.Put(data)
			mlog.Infof("session input chan is full %d", sess.convId)
		}
	} else {
		select {
		case s.acceptCh <- &handshakeTask{data: data, raddr: from}:
		default:
			// 丢弃
			defaultBufferPool.Put(data)
			mlog.Info("server accept chan is full")
		}
	}
}

type handshakeTask struct {
	data  []byte
	raddr net.Addr
}

func (s *Server) acceptLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case task := <-s.acceptCh:
			if cli, convId, ok := s.opt.OnHandshake(task.data, task.raddr); ok && convId != 0 {
				sess := newSession(convId, task.raddr, cli, s)
				s.sessMtx.Lock()
				s.sessions[convId] = sess
				s.sessMtx.Unlock()

				cli.OnConnect(sess)
			}
		}
	}
}

func (s *Server) closeSession(sess *Session) {
	s.sessMtx.Lock()
	delete(s.sessions, sess.convId)
	s.sessMtx.Unlock()
}

func (s *Server) writeLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case out := <-s.writeChan:
			mlog.Trace("server write ", len(out.data))
			if _, err := s.conn.WriteTo(out.data, out.raddr); err != nil {
				mlog.Errorf("server WriteTo %s err:%v", out.raddr.String(), err)
			}
		}
	}
}

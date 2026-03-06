package kcp

import (
	"context"
	"net"
	"sync"
)

type Server struct {
	opt       *ServerOptions
	conn      net.PacketConn
	acceptCh  chan *handshakeTask
	writeChan chan []byte
	sessions  map[string]*Session
	sessMtx   sync.Mutex
	ctx       context.Context
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
		writeChan: make(chan []byte, 1024),
		sessions:  make(map[string]*Session),
		ctx:       context.Background(),
		wg:        &sync.WaitGroup{},
	}
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
	return nil
}

func (s *Server) readLoop() {
	buf := make([]byte, mtuLimit)
	for {
		n, from, err := s.conn.ReadFrom(buf)
		if err != nil {
			//s.notifyReadError(err)
			return
		}

		s.onRead(buf[:n], from)
	}
}

func (s *Server) onRead(buf []byte, from net.Addr) {
	if len(buf) == 0 {
		return
	}
	data := defaultBufferPool.Get()[:len(buf)]
	copy(data, buf)
	defer defaultBufferPool.Put(data)

	raddr := from.String()
	s.sessMtx.Lock()
	sess, exist := s.sessions[raddr]
	s.sessMtx.Unlock()
	if exist {
		select {
		case sess.inputCh <- data:
		default:
			// 丢弃
			// TODO log
		}
	} else {
		select {
		case s.acceptCh <- &handshakeTask{data: data, raddr: raddr}:
		default:
			// TODO log
		}
	}
}

type handshakeTask struct {
	data  []byte
	raddr string
}

func (s *Server) acceptLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case task := <-s.acceptCh:
			raddr := task.raddr
			if cli, convId, ok := s.opt.OnHandshake(task.data, task.raddr); ok && convId != 0 {
				sess := newSession(convId, raddr, s)
				s.sessMtx.Lock()
				s.sessions[raddr] = sess
				s.sessMtx.Unlock()

				cli.OnConnect(sess)
			}
		}
	}
}

func (s *Server) writeLoop() {
	for {
		select {
		case <-s.ctx.Done():
		case data := <-s.writeChan:
			if _, err := s.conn.WriteTo(data, nil); err != nil {
				// TODO log
			}
			defaultBufferPool.Put(data)
		}
	}
}

package wsg

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/fixkme/gokit/ds/staticlist"
	"github.com/fixkme/gokit/mlog"
	"github.com/panjf2000/gnet/v2"
)

const (
	maxHandshakeLen    = 1 << 20  // 默认 握手请求报文大小上限 1M
	maxPayloadSize     = 64 << 20 // 默认 用户数据大小上限 64M
	maxHandshakingSize = 1024     // 默认 正在握手的连接数量上限
)

type ServerOptions struct {
	gnet.Options

	// 监听地址，类似"tcp://127.0.0.1:2333"
	Addr string

	// 握手请求报文大小上限
	MaxHandshakeLen int64
	// 用户数据大小上限
	MaxPayloadSize int64

	// 握手超时时间，毫秒ms
	// 出于灵活性考虑，当>0才有效，=0无效时，用户层可以通过心跳处理握手超时
	HandshakeTimeout int64
	// 正在握手的连接数量上限
	MaxHandshakingSize int64

	// 建立连接时的回调
	// 在io协程中执行，所以回调尽量不要阻塞，否则会影响其他连接
	OnClientConnect func(conn *Conn) error

	// 握手参数
	Upgrader *Upgrader
	// 握手成功回调
	OnHandshake func(conn *Conn, r *http.Request) error

	// 连接关闭时回调
	// 在io协程中执行，所以回调尽量不要阻塞，否则会影响其他连接
	// 无法区分是客户端退出还是server退出导致的关闭, 需要用户自己区分
	// 注意：握手失败导致的关闭，conn.session == nil
	OnClientClose func(conn *Conn, err error)

	// server退出后回调
	OnServerShutdown func(gnet.Engine)
}

type Server struct {
	gnet.BuiltinEventEngine
	gnet.Engine      // use for stop
	opt              *ServerOptions
	handshakingQueue *hsQueue // 握手超时处理
	handshakingWait  chan *handshakingConn
	handshakingOver  chan *Conn
}

type handshakingConn struct {
	deadline int64 //ms
	conn     *Conn
}
type hsQueue = staticlist.Queue[*handshakingConn]
type hsQNode = staticlist.QNode[*handshakingConn]

func NewServer(opt *ServerOptions) *Server {
	if opt.Upgrader == nil {
		opt.Upgrader = &Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
	}
	if opt.MaxHandshakeLen <= 0 {
		opt.MaxHandshakeLen = maxHandshakeLen
	}
	if opt.MaxPayloadSize <= 0 {
		opt.MaxPayloadSize = maxPayloadSize
	}
	if opt.MaxHandshakingSize <= 0 {
		opt.MaxHandshakingSize = maxHandshakingSize
	}

	srv := &Server{
		opt: opt,
	}
	if srv.needDealWithHandshakeingTimeout() {
		srv.handshakingQueue = staticlist.NewQueue[*handshakingConn](int(opt.MaxHandshakingSize))
		srv.handshakingWait = make(chan *handshakingConn, 1024)
		srv.handshakingOver = make(chan *Conn, 1024)
	}
	return srv
}

func (s *Server) Run() error {
	if s.needDealWithHandshakeingTimeout() {
		quit := make(chan struct{})
		defer close(quit)
		go s.runTicker(quit)
	}

	if err := gnet.Run(s, s.opt.Addr, gnet.WithOptions(s.opt.Options)); err != nil {
		return err
	}
	return nil
}

// 在gnet.Run协程里被调用
func (s *Server) OnBoot(eng gnet.Engine) (action gnet.Action) {
	s.Engine = eng
	return
}

// 在gnet.Run协程里被调用
func (s *Server) OnShutdown(eng gnet.Engine) {
	if cb := s.opt.OnServerShutdown; cb != nil {
		cb(eng)
	}
}

func (s *Server) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	conn := newConn(c)
	c.SetContext(conn)

	if cb := s.opt.OnClientConnect; cb != nil {
		if err := cb(conn); err != nil {
			action = gnet.Close
			return
		}
	}

	if s.needDealWithHandshakeingTimeout() {
		timeout := s.opt.HandshakeTimeout
		hconn := &handshakingConn{
			deadline: time.Now().UnixMilli() + timeout,
			conn:     conn,
		}
		select {
		case s.handshakingWait <- hconn:
		default:
			ReplyHttpError(c, nil, http.StatusServiceUnavailable, "server busy")
			action = gnet.Close
		}
	}

	return
}

func (s *Server) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	if cb := s.opt.OnClientClose; cb != nil {
		if conn, ok := c.Context().(*Conn); ok {
			cb(conn, err)
		}
	}
	return
}

func (s *Server) OnTraffic(c gnet.Conn) (r gnet.Action) {
	conn := c.Context().(*Conn)
	// 处理握手
	if !conn.isUpgrade {
		if err := s.handshake(conn); err != nil {
			mlog.Error("ws handshake failed, %v", err)
			s.tryPromptlyRemoveHandshakingQnode(conn)
			return gnet.Close
		}
		return
	}
	// 处理websocket帧
	if err := s.readFrame(conn); err != nil {
		mlog.Error("read ws frame err: %v", err)
		return gnet.Close
	}
	return
}

var crlf = []byte("\r\n\r\n")

func (s *Server) handshake(conn *Conn) error {
	c := conn.Conn
	if c.InboundBuffered() > int(s.opt.MaxHandshakeLen) {
		return errHandshakeDataTooLarge
	}
	datas, err := c.Peek(-1)
	if err != nil {
		if err == io.ErrShortBuffer {
			return nil
		}
		return err
	}
	endIdx := bytes.LastIndex(datas, crlf)
	if endIdx == -1 {
		return nil
	}

	c.Discard(len(datas))

	reader := bufio.NewReader(bytes.NewReader(datas))
	req, err := http.ReadRequest(reader)
	if err != nil {
		ReplyHttpError(c, req, http.StatusBadRequest, err.Error())
		return err
	}
	if reader.Buffered() > 0 {
		// RFC 6455：客户端必须等待服务器握手响应后才能发送帧数据
		err = errHandshakeSendFrameData
		ReplyHttpError(c, req, http.StatusBadRequest, err.Error())
		return err
	}

	if err = s.opt.Upgrader.Upgrade(conn, req, nil); err != nil {
		return err
	}
	conn.isUpgrade = true
	conn.upgraded.Store(true)
	s.tryPromptlyRemoveHandshakingQnode(conn)

	// 握手成功回调
	if cb := s.opt.OnHandshake; cb != nil {
		if err = cb(conn, req); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) readFrame(conn *Conn) (err error) {
	c := conn.Conn
	var wsh *WsHead
	var payload []byte
	defer func() {
		if err != nil && err != io.ErrShortBuffer {
			if wsh = conn.wsHead; wsh != nil {
				conn.wsHead = nil
				wsHeadPool.Put(wsh)
			}
		}
	}()

	for {
		if !conn.wsHeadOk {
			ok, _err := conn.ReadWsHeader()
			if err = _err; err != nil {
				return
			} else if !ok {
				return
			}
			conn.wsHeadOk = true
			wsh = conn.wsHead
			if !wsh.Masked {
				//客户端必须有mask
				err = errors.New("must masked")
				ReplyWsError(c, 1002, err)
				return
			}
			if wsh.OpCode == OpText {
				//不支持text数据
				err = errNotSupportTextData
				ReplyWsError(c, 1002, err)
				return
			}
		} else {
			wsh = conn.wsHead
		}

		if wsh.Length > 0 {
			if int(wsh.Length)+len(conn.buff) > int(s.opt.MaxPayloadSize) {
				err = errPayloadTooLarge
				ReplyWsError(c, 1009, err)
				return
			}
			payload, err = c.Next(int(wsh.Length))
			if err != nil {
				if err == io.ErrShortBuffer {
					err = nil
				}
				return
			}
			MaskWsPayload(wsh.Mask, payload)
		}

		// 优先处理控制帧
		if IsControlOp(wsh.OpCode) {
			if err = conn.processOpFrame(wsh, payload); err != nil {
				return
			}
		} else if len(payload) > 0 {
			if conn.buff == nil {
				conn.buff = make([]byte, 0, len(payload))
			}
			// 必须深拷贝，因为需要跨协程处理，payload可能来自c.buffer引用
			conn.buff = append(conn.buff, payload...)
			if wsh.Fin {
				// 投递给路由协程处理, 或者就在本io协程处理，取决于router.PushData实现
				// 无论是投递给路由协程，还是直接处理，都不要阻塞，否则可能影响其他conn
				conn.router.PushData(conn.session, conn.buff[:])
				conn.buff = nil
			}
		}
		// 进行下一帧处理
		payload = nil
		conn.wsHeadOk = false
		conn.wsHeadLen = 0
		conn.wsHead = nil
		wsHeadPool.Put(wsh)
		wsh = nil
	}
}

func (s *Server) runTicker(quit <-chan struct{}) {
	tickerInterval := 5 * time.Second
	dur := time.Duration(s.opt.HandshakeTimeout) * time.Millisecond
	if tickerInterval > dur {
		tickerInterval = dur
	}
	timer := time.NewTimer(tickerInterval)
	for {
		select {
		case <-quit:
			return
		case c, ok := <-s.handshakingWait:
			mlog.Debug("handshakingWait %v, %v", c.conn.IsUpgraded(), c.conn.RemoteAddr())
			if ok && c.conn.IsUpgraded() == false {
				if node := s.handshakingQueue.Push(c); node != nil {
					c.conn.qnode.Store(node)
				} else {
					mlog.Warn("handshakingQueue full, %v", c.conn.RemoteAddr())
					c.conn.Close()
				}
			}
		case conn := <-s.handshakingOver:
			if conn != nil && conn.qnode.Load() != nil {
				if node := conn.qnode.Swap(nil); node != nil {
					mlog.Debug("handshakingOver %v, %v", conn.IsUpgraded(), conn.RemoteAddr())
					s.handshakingQueue.Remove(node)
				}
			}
		case tm := <-timer.C:
			nextDur := s.updateHandshakingQueue(tm.UnixMilli())
			if nextDur == 0 {
				nextDur = tickerInterval
			}
			timer.Reset(nextDur)
		}
	}
}

func (s *Server) updateHandshakingQueue(nowMs int64) (nextDur time.Duration) {
	minDur := int64(10)
	s.handshakingQueue.PopRange(func(v **handshakingConn) bool {
		c := *v
		if c.deadline > nowMs {
			delta := c.deadline - nowMs
			if delta < minDur { // 放开10ms
				delta = minDur
			}
			nextDur = time.Duration(delta) * time.Millisecond
			return false
		}
		if c.conn.IsUpgraded() == false {
			mlog.Info("handshake timeout %v", c.conn.RemoteAddr())
			c.conn.Close()
		}
		c.conn.qnode.Store(nil)
		return true
	})
	//mlog.Debug("updateHandshakingQueue size:%d,  %v", s.handshakingQueue.Len(), time.Now())
	return
}

func (s *Server) tryPromptlyRemoveHandshakingQnode(conn *Conn) {
	if s.needDealWithHandshakeingTimeout() {
		if qnode := conn.qnode.Load(); qnode != nil {
			select {
			case s.handshakingOver <- conn: // 及时删除QNode
			default: // 最迟在ticker里移除QNode
			}
		}
	}
}

func (s *Server) needDealWithHandshakeingTimeout() bool {
	return s.opt.HandshakeTimeout > 0
}

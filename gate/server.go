package gate

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/panjf2000/gnet/v2"
)

type ServerOptions struct {
	gnet.Options
	Addr             string //"tcp://127.0.0.1:2333"
	HandshakeTimeout time.Duration
	FragmentSize     int
	OnClientClose    func(conn *Conn, err error)
}

type Server struct {
	gnet.BuiltinEventEngine
	gnet.Engine
	opt   *ServerOptions
	rpool RouterPool
}

func NewServer(opt *ServerOptions, rpool RouterPool) *Server {
	return &Server{opt: opt, rpool: rpool}
}

func (s *Server) Run() {
	quit := make(chan struct{})
	s.rpool.Start(quit)
	if err := gnet.Run(s, s.opt.Addr, gnet.WithOptions(s.opt.Options)); err != nil {
		log.Fatalf("Server Run with error: %v", err)
	}
}

func (s *Server) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	conn := &Conn{c: c}
	c.SetContext(conn)
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
	if !conn.upgraded {
		datas, err := c.Next(-1)
		if err != nil && err != io.ErrShortBuffer {
			log.Printf("conn.Next err:%v", err)
			return gnet.Close
		}
		conn.buff = append(conn.buff, datas...)
		endIdx := bytes.LastIndex(datas, []byte("\r\n\r\n"))
		if endIdx == -1 {
			return
		}
		reader := bufio.NewReader(bytes.NewReader(conn.buff))
		req, err := http.ReadRequest(reader)
		if err != nil {
			ReplyHttpError(c, req, http.StatusBadRequest, "")
			return gnet.Close
		}
		if reader.Buffered() > 0 {
			ReplyHttpError(c, req, http.StatusBadRequest, "websocket: client sent data before handshake is complete")
			return gnet.Close
		}

		upgrader := Upgrader{HandshakeTimeout: time.Second}
		if err := upgrader.Upgrade(conn, req, nil); err != nil {
			return gnet.Close
		}
		conn.upgraded = true
		conn.buff = nil
		// 握手成功回调
		if err := s.rpool.OnHandshake(conn, req); err != nil {
			return gnet.Close
		}
		return
	}
	// 处理websocket帧
	var err error
	var wsh *WsHead
	var payload []byte
	defer func() {
		if err != nil && err != io.ErrShortBuffer {
			fmt.Println("err:", err)
			if wsh = conn.wsHead; wsh != nil {
				conn.wsHead = nil
				wsHeadPool.Put(wsh)
			}
		}
	}()

	for {
		if !conn.wsHeadOk {
			ok, _err := conn.ReadWsHeader(c)
			if err = _err; err != nil {
				return gnet.Close
			} else if !ok {
				return gnet.None
			}
			conn.wsHeadOk = true
			wsh = conn.wsHead
			if !wsh.Masked {
				//客户端必须有mask
				err = errors.New("must masked")
				ReplyWsError(c, 1002, err)
				return gnet.Close
			}
			if wsh.OpCode == OpText {
				//不支持text数据
				err = errors.New("not support text data")
				ReplyWsError(c, 1002, err)
				return gnet.Close
			}
		} else {
			wsh = conn.wsHead
		}

		if wsh.Length > 0 {
			payload, err = c.Next(int(wsh.Length))
			if err != nil {
				if err != io.ErrShortBuffer {
					return gnet.Close
				}
				err = nil
				return
			}
			MaskWsPayload(wsh.Mask, payload)
		}

		// 优先处理控制帧
		if IsControlOp(wsh.OpCode) {
			if err = conn.processOpFrame(wsh, payload); err != nil {
				return gnet.Close
			}
		} else if len(payload) > 0 {
			if conn.buff == nil {
				conn.buff = make([]byte, 0, len(payload))
			} else {
				if len(conn.buff)+len(payload) > math.MaxInt16 {
					fmt.Println("datas too long")
				}
			}
			conn.buff = append(conn.buff, payload...)
			if wsh.Fin {
				// 交给task处理
				conn.router.PushData(conn.cli, conn.buff[:])
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

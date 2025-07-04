package wsg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/fixkme/gokit/mlog"
	"github.com/panjf2000/gnet/v2"
	"github.com/panjf2000/gnet/v2/pkg/pool/byteslice"
)

type Conn struct {
	gnet.Conn
	upgraded  bool
	buff      []byte
	wsHeadOk  bool
	wsHeadLen int
	wsHead    *WsHead

	session any
	router  RoutingWorker
}

func (conn *Conn) BindRoutingWorker(r RoutingWorker) {
	conn.router = r
}

func (conn *Conn) BindSession(session any) {
	conn.session = session
}

func (conn *Conn) GetSession() any {
	return conn.session
}

func (conn *Conn) ReadWsHeader(c gnet.Conn) (headOk bool, err error) {
	var bts []byte
	const front = 2
	var payloadLen byte
	var h *WsHead
	if conn.wsHeadLen == 0 {
		bts, err = c.Peek(front)
		if err != nil {
			return false, nil
		}

		h = wsHeadPool.Get()
		h.Fin = bts[0]&0x80 != 0
		h.Rsv = (bts[0] & 0x70) >> 4
		h.OpCode = byte(bts[0] & 0x0f)

		var extra int
		if bts[1]&0x80 != 0 {
			h.Masked = true
			extra += 4
		}

		payloadLen = bts[1] & 0x7f
		switch {
		case payloadLen < 126:
			h.Length = int64(payloadLen)
		case payloadLen == 126:
			extra += 2
		case payloadLen == 127:
			extra += 8
		default:
			err = fmt.Errorf("ErrHeaderLengthUnexpected")
			return
		}

		conn.wsHead = h
		conn.wsHeadLen = front + extra
		if extra == 0 {
			c.Discard(front)
			return true, nil
		}
	} else {
		h = conn.wsHead
	}

	bts, err = c.Next(conn.wsHeadLen)
	if err != nil {
		if err == io.ErrShortBuffer {
			return false, nil
		}
		return false, err
	}
	payloadLen = bts[1] & 0x7f
	bts = bts[front:]
	switch {
	case payloadLen == 126:
		h.Length = int64(binary.BigEndian.Uint16(bts[:2]))
		bts = bts[2:]

	case payloadLen == 127:
		if bts[0]&0x80 != 0 {
			err = fmt.Errorf("ErrHeaderLengthMSB")
			return
		}
		h.Length = int64(binary.BigEndian.Uint64(bts[:8]))
		bts = bts[8:]
	}

	if h.Masked {
		copy(h.Mask[:], bts)
	}
	return true, nil
}

func (conn *Conn) Send(content []byte) (err error) {
	if len(content) == 0 {
		return
	}
	wsh := wsHeadPool.Get()
	wsh.Fin = true
	wsh.OpCode = OpBinary
	wsh.Length = int64(len(content))
	defer wsHeadPool.Put(wsh)

	hbuff, err := MakeWsHeadBuff(wsh)
	if err != nil {
		return err
	}
	err = conn.AsyncWritev([][]byte{hbuff, content}, func(c gnet.Conn, err error) error {
		if err == nil {
			byteslice.Put(hbuff)
		} else {
			mlog.Error("ws AsyncWritev error: %v", err)
		}
		return nil
	})
	return
}

func (conn *Conn) innerSendWsOpFrame(op byte, payload []byte) (err error) {
	wsh := wsHeadPool.Get()
	wsh.Fin = true
	wsh.OpCode = op
	wsh.Length = int64(len(payload))
	hbuff, _ := MakeWsHeadBuff(wsh)
	defer func() {
		wsHeadPool.Put(wsh)
		byteslice.Put(hbuff)
	}()

	if len(payload) == 0 {
		if _, err = conn.Write(hbuff); err != nil {
			return
		}
	} else {
		if _, err = conn.Writev([][]byte{hbuff, payload}); err != nil {
			return
		}
	}
	return
}

func (conn *Conn) processOpFrame(wsh *WsHead, payload []byte) (err error) {
	if !wsh.Fin {
		ReplyWsError(conn, 1002, errInvalidControlFrame)
		return errInvalidControlFrame
	}
	switch wsh.OpCode {
	case OpClose:
		if len(payload) == 1 || len(payload) > 125 {
			ReplyWsError(conn, 1002, errInvalidControlFrame)
			return errInvalidControlFrame
		}
		if len(payload) >= 2 {
			statusCode := binary.BigEndian.Uint16(payload)
			var reason string
			if len(payload) > 2 {
				reason = string(payload[2:])
			}
			fmt.Printf("对方关闭ws: %d, %s\n", statusCode, reason)
		}
		conn.innerSendWsOpFrame(OpClose, payload)
		return errors.New("client ws closed")
	case OpPing:
		if len(payload) > 125 {
			ReplyWsError(conn, 1002, errInvalidControlFrame)
			return errInvalidControlFrame
		}
		err = conn.innerSendWsOpFrame(OpPong, payload)
	case OpPong:
		// none
	}
	return
}

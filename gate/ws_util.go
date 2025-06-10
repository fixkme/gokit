package gate

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/panjf2000/gnet/v2/pkg/pool/byteslice"
)

const (
	MaxWsHeaderSize      = 14
	OpContinuation  byte = 0x0
	OpText          byte = 0x1
	OpBinary        byte = 0x2
	OpClose         byte = 0x8
	OpPing          byte = 0x9
	OpPong          byte = 0xa
)

type WsHead struct {
	Fin    bool
	Rsv    byte
	OpCode byte

	Masked bool
	Length int64

	Mask [4]byte
}

type WsHeadPool struct {
	sync.Pool
}

var wsHeadPool = &WsHeadPool{
	Pool: sync.Pool{New: func() interface{} {
		return new(WsHead)
	}},
}

func (p *WsHeadPool) Get() *WsHead {
	return p.Pool.Get().(*WsHead)
}

func (p *WsHeadPool) Put(h *WsHead) {
	h.Fin = false
	h.Rsv = 0
	h.OpCode = 0
	h.Masked = false
	h.Length = 0
	clear(h.Mask[:])
	p.Pool.Put(h)
}

func ReadWsHeader(r io.Reader) (h *WsHead, err error) {
	bts := make([]byte, 2, MaxWsHeaderSize-2)
	_, err = io.ReadFull(r, bts)
	if err != nil {
		return
	}

	h = new(WsHead)
	h.Fin = bts[0]&0x80 != 0
	h.Rsv = (bts[0] & 0x70) >> 4
	h.OpCode = byte(bts[0] & 0x0f)

	var extra int

	if bts[1]&0x80 != 0 {
		h.Masked = true
		extra += 4
	}

	length := bts[1] & 0x7f
	switch {
	case length < 126:
		h.Length = int64(length)

	case length == 126:
		extra += 2

	case length == 127:
		extra += 8

	default:
		err = fmt.Errorf("ErrHeaderLengthUnexpected")
		return
	}

	if extra == 0 {
		return
	}

	bts = bts[:extra]
	_, err = io.ReadFull(r, bts)
	if err != nil {
		return
	}

	switch {
	case length == 126:
		h.Length = int64(binary.BigEndian.Uint16(bts[:2]))
		bts = bts[2:]

	case length == 127:
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
	return
}

func MaskWsPayload(mask [4]byte, payload []byte) {
	for i := 0; i < len(payload); i++ {
		payload[i] = payload[i] ^ mask[i&3]
	}
}

func MakeWsHeadBuff(h *WsHead) (buff []byte, err error) {
	bts := byteslice.Get(MaxWsHeaderSize)
	//bts := make([]byte, MaxWsHeaderSize)
	if h.Fin {
		bts[0] |= 0x80
	}
	bts[0] |= h.Rsv << 4
	bts[0] |= byte(h.OpCode)

	var n int
	switch {
	case h.Length <= 125:
		bts[1] = byte(h.Length)
		n = 2

	case h.Length <= (1<<16)-1:
		bts[1] = 126
		binary.BigEndian.PutUint16(bts[2:4], uint16(h.Length))
		n = 4

	case h.Length <= (1<<63)-1:
		bts[1] = 127
		binary.BigEndian.PutUint64(bts[2:10], uint64(h.Length))
		n = 10

	default:
		return nil, fmt.Errorf("ErrHeaderLengthUnexpected")
	}

	if h.Masked {
		bts[1] |= 0x80
		n += copy(bts[n:], h.Mask[:])
	}
	buff = bts[:n]
	return
}

func MakeWsClosePayload(statusCode uint16, reason string) ([]byte, error) {
	if len(reason) > 123 {
		return nil, fmt.Errorf("ErrCloseReasonTooLong")
	}
	p := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(p[:2], 1002)
	p = append(p, reason...)
	return p, nil
}

func ReplyWsError(w io.Writer, statusCode uint16, reason error) (err error) {
	var reasonDesc string
	if reason != nil {
		reasonDesc = reason.Error()
	}
	wsh := wsHeadPool.Get()
	wsh.Fin = true
	wsh.OpCode = OpClose
	wsh.Length = int64(2 + len(reasonDesc))
	hbuff, _ := MakeWsHeadBuff(wsh)
	defer func() {
		wsHeadPool.Put(wsh)
		byteslice.Put(hbuff)
	}()

	if _, err = w.Write(hbuff); err != nil {
		return
	}
	payload, _ := MakeWsClosePayload(statusCode, reasonDesc)
	if _, err = w.Write(payload); err != nil {
		return
	}
	return
}

func ReplyHttpError(w io.Writer, r *http.Request, status int, reason string) {
	statusCode := strconv.Itoa(status)
	statusDesc := http.StatusText(status)
	p := make([]byte, 0, 74+len(statusCode)+len(statusDesc)+len(reason))
	p = append(p, "HTTP/1.1 "...)
	p = append(p, statusCode...)
	p = append(p, ' ')
	p = append(p, statusDesc...)
	p = append(p, "\r\n"...)
	p = append(p, "Content-Type: text/plain; charset=utf-8\r\n"...)
	p = append(p, "Connection: close\r\n\r\n"...)
	p = append(p, reason...)
	fmt.Printf("ReplyHttpError cap %d, len: %d, %v\n", cap(p), len(p), string(p))
	w.Write(p)
}

func HttpHeaderGetInt64(h http.Header, key string) int64 {
	str := h.Get(key)
	if str == "" {
		return 0
	}
	v, err := strconv.ParseInt(str, 10, 64)
	if err == nil {
		return v
	}
	return 0
}

func IsControlOp(op byte) bool {
	// 0x8、0x9、0xa为控制帧
	return op&0x8 != 0
}

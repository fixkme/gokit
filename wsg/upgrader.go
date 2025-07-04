package wsg

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type HandshakeError struct {
	message string
}

func (e HandshakeError) Error() string { return e.message }

type Upgrader struct {
	HandshakeTimeout time.Duration

	// ReadBufferSize and WriteBufferSize specify I/O buffer sizes in bytes. If a buffer
	// size is zero, then buffers allocated by the HTTP server are used. The
	// I/O buffer sizes do not limit the size of the messages that can be sent
	// or received.
	// ReadBufferSize, WriteBufferSize int

	// WriteBufferPool is a pool of buffers for write operations. If the value
	// is not set, then write buffers are allocated to the connection for the
	// lifetime of the connection.
	//
	// A pool is most useful when the application has a modest volume of writes
	// across a large number of connections.
	//
	// Applications should use a single pool for each unique value of
	// WriteBufferSize.
	// WriteBufferPool BufferPool

	// Subprotocols specifies the server's supported protocols in order of
	// preference. If this field is not nil, then the Upgrade method negotiates a
	// subprotocol by selecting the first match in this list with a protocol
	// requested by the client. If there's no match, then no protocol is
	// negotiated (the Sec-Websocket-Protocol header is not included in the
	// handshake response).
	Subprotocols []string

	// CheckOrigin returns true if the request Origin header is acceptable. If
	// CheckOrigin is nil, then a safe default is used: return false if the
	// Origin request header is present and the origin host is not equal to
	// request Host header.
	//
	// A CheckOrigin function should carefully validate the request origin to
	// prevent cross-site request forgery.
	CheckOrigin func(r *http.Request) bool

	// EnableCompression specify if the server should attempt to negotiate per
	// message compression (RFC 7692). Setting this value to true does not
	// guarantee that compression will be supported. Currently only "no context
	// takeover" modes are supported.
	// EnableCompression bool
}

func (u *Upgrader) returnError(w io.Writer, r *http.Request, status int, reason string) error {
	ReplyHttpError(w, r, status, reason)
	return HandshakeError{reason}
}

func (u *Upgrader) selectSubprotocol(r *http.Request, responseHeader http.Header) string {
	if u.Subprotocols != nil {
		clientProtocols := Subprotocols(r)
		for _, serverProtocol := range u.Subprotocols {
			for _, clientProtocol := range clientProtocols {
				if clientProtocol == serverProtocol {
					return clientProtocol
				}
			}
		}
	} else if responseHeader != nil {
		return responseHeader.Get("Sec-Websocket-Protocol")
	}
	return ""
}

func (u *Upgrader) Upgrade(conn *Conn, r *http.Request, responseHeader http.Header) error {
	w := conn
	const badHandshake = "websocket: the client is not using the websocket protocol: "
	if r.Method != http.MethodGet {
		return u.returnError(w, r, http.StatusMethodNotAllowed, badHandshake+"request method is not GET")
	}

	if !tokenListContainsValue(r.Header, "Connection", "upgrade") {
		return u.returnError(w, r, http.StatusBadRequest, badHandshake+"'upgrade' token not found in 'Connection' header")
	}

	if !tokenListContainsValue(r.Header, "Upgrade", "websocket") {
		return u.returnError(w, r, http.StatusBadRequest, badHandshake+"'websocket' token not found in 'Upgrade' header")
	}

	if !tokenListContainsValue(r.Header, "Sec-Websocket-Version", "13") {
		return u.returnError(w, r, http.StatusBadRequest, "websocket: unsupported version: 13 not found in 'Sec-Websocket-Version' header")
	}

	if _, ok := responseHeader["Sec-Websocket-Extensions"]; ok {
		return u.returnError(w, r, http.StatusInternalServerError, "websocket: application specific 'Sec-WebSocket-Extensions' headers are unsupported")
	}

	checkOrigin := u.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = checkSameOrigin
	}
	if !checkOrigin(r) {
		return u.returnError(w, r, http.StatusForbidden, "websocket: request origin not allowed by Upgrader.CheckOrigin")
	}

	challengeKey := r.Header.Get("Sec-Websocket-Key")
	if !isValidChallengeKey(challengeKey) {
		return u.returnError(w, r, http.StatusBadRequest, "websocket: not a websocket handshake: 'Sec-WebSocket-Key' header must be Base64 encoded value of 16-byte in length")
	}

	// subprotocol := u.selectSubprotocol(r, responseHeader)

	// // Negotiate PMCE
	// var compress bool
	// if u.EnableCompression {
	// 	for _, ext := range parseExtensions(r.Header) {
	// 		if ext[""] != "permessage-deflate" {
	// 			continue
	// 		}
	// 		compress = true
	// 		break
	// 	}
	// }

	// h, ok := w.(http.Hijacker)
	// if !ok {
	// 	return u.returnError(w, r, http.StatusInternalServerError, "websocket: response does not implement http.Hijacker")
	// }
	// var brw *bufio.ReadWriter
	// netConn, brw, err := h.Hijack()
	// if err != nil {
	// 	return u.returnError(w, r, http.StatusInternalServerError, err.Error())
	// }

	// if brw.Reader.Buffered() > 0 {
	// 	netConn.Close()
	// 	return errors.New("websocket: client sent data before handshake is complete")
	// }

	// var br *bufio.Reader
	// if u.ReadBufferSize == 0 && bufioReaderSize(netConn, brw.Reader) > 256 {
	// 	// Reuse hijacked buffered reader as connection reader.
	// 	br = brw.Reader
	// }

	// buf := bufioWriterBuffer(netConn, brw.Writer)

	// var writeBuf []byte
	// if u.WriteBufferPool == nil && u.WriteBufferSize == 0 && len(buf) >= maxFrameHeaderSize+256 {
	// 	// Reuse hijacked write buffer as connection buffer.
	// 	writeBuf = buf
	// }

	// c := newConn(netConn, true, u.ReadBufferSize, u.WriteBufferSize, u.WriteBufferPool, br, writeBuf)
	// c.subprotocol = subprotocol

	// if compress {
	// 	c.newCompressionWriter = compressNoContextTakeover
	// 	c.newDecompressionReader = decompressNoContextTakeover
	// }

	// // Use larger of hijacked buffer and connection write buffer for header.
	// p := buf
	// if len(c.writeBuf) > len(p) {
	// 	p = c.writeBuf
	// }
	// p = p[:0]

	const specific = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "
	p := make([]byte, 0, len(specific)+32) // 32=28sign+2+2
	p = append(p, specific...)
	p = append(p, computeAcceptKey(challengeKey)...)
	p = append(p, "\r\n\r\n"...)
	// if c.subprotocol != "" {
	// 	p = append(p, "Sec-WebSocket-Protocol: "...)
	// 	p = append(p, c.subprotocol...)
	// 	p = append(p, "\r\n"...)
	// }
	// if compress {
	// 	p = append(p, "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n"...)
	// }
	// for k, vs := range responseHeader {
	// 	if k == "Sec-Websocket-Protocol" {
	// 		continue
	// 	}
	// 	for _, v := range vs {
	// 		p = append(p, k...)
	// 		p = append(p, ": "...)
	// 		for i := 0; i < len(v); i++ {
	// 			b := v[i]
	// 			if b <= 31 {
	// 				// prevent response splitting.
	// 				b = ' '
	// 			}
	// 			p = append(p, b)
	// 		}
	// 		p = append(p, "\r\n"...)
	// 	}
	// }
	// p = append(p, "\r\n"...)

	if u.HandshakeTimeout > 0 {
		w.SetWriteDeadline(time.Now().Add(u.HandshakeTimeout))
	}
	if _, err := w.Write(p); err != nil {
		return err
	}
	if u.HandshakeTimeout > 0 {
		// Clear deadlines.
		w.SetWriteDeadline(time.Time{})
	}

	return nil
}

// Subprotocols returns the subprotocols requested by the client in the
// Sec-Websocket-Protocol header.
func Subprotocols(r *http.Request) []string {
	h := strings.TrimSpace(r.Header.Get("Sec-Websocket-Protocol"))
	if h == "" {
		return nil
	}
	protocols := strings.Split(h, ",")
	for i := range protocols {
		protocols[i] = strings.TrimSpace(protocols[i])
	}
	return protocols
}

// writeHook is an io.Writer that records the last slice passed to it vio
// io.Writer.Write.
type writeHook struct {
	p []byte
}

func (wh *writeHook) Write(p []byte) (int, error) {
	wh.p = p
	return len(p), nil
}

// bufioWriterBuffer grabs the buffer from a bufio.Writer.
func bufioWriterBuffer(originalWriter io.Writer, bw *bufio.Writer) []byte {
	// This code assumes that bufio.Writer.buf[:1] is passed to the
	// bufio.Writer's underlying writer.
	var wh writeHook
	bw.Reset(&wh)
	bw.WriteByte(0)
	bw.Flush()

	bw.Reset(originalWriter)

	return wh.p[:cap(wh.p)]
}

// checkSameOrigin returns true if the origin is not set or is equal to the request host.
func checkSameOrigin(r *http.Request) bool {
	origin := r.Header["Origin"]
	if len(origin) == 0 {
		return true
	}
	u, err := url.Parse(origin[0])
	if err != nil {
		return false
	}
	return equalASCIIFold(u.Host, r.Host)
}

func tokenListContainsValue(header http.Header, name string, value string) bool {
headers:
	for _, s := range header[name] {
		for {
			var t string
			t, s = nextToken(skipSpace(s))
			if t == "" {
				continue headers
			}
			s = skipSpace(s)
			if s != "" && s[0] != ',' {
				continue headers
			}
			if equalASCIIFold(t, value) {
				return true
			}
			if s == "" {
				continue headers
			}
			s = s[1:]
		}
	}
	return false
}

// skipSpace returns a slice of the string s with all leading RFC 2616 linear
// whitespace removed.
func skipSpace(s string) (rest string) {
	i := 0
	for ; i < len(s); i++ {
		if b := s[i]; b != ' ' && b != '\t' {
			break
		}
	}
	return s[i:]
}

func equalASCIIFold(s, t string) bool {
	for s != "" && t != "" {
		sr, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		tr, size := utf8.DecodeRuneInString(t)
		t = t[size:]
		if sr == tr {
			continue
		}
		if 'A' <= sr && sr <= 'Z' {
			sr = sr + 'a' - 'A'
		}
		if 'A' <= tr && tr <= 'Z' {
			tr = tr + 'a' - 'A'
		}
		if sr != tr {
			return false
		}
	}
	return s == t
}

func nextToken(s string) (token, rest string) {
	i := 0
	for ; i < len(s); i++ {
		if !isTokenOctet[s[i]] {
			break
		}
	}
	return s[:i], s[i:]
}

func isValidChallengeKey(s string) bool {
	// From RFC6455:
	//
	// A |Sec-WebSocket-Key| header field with a base64-encoded (see
	// Section 4 of [RFC4648]) value that, when decoded, is 16 bytes in
	// length.

	if s == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	return err == nil && len(decoded) == 16
}

// Token octets per RFC 2616.
var isTokenOctet = [256]bool{
	'!':  true,
	'#':  true,
	'$':  true,
	'%':  true,
	'&':  true,
	'\'': true,
	'*':  true,
	'+':  true,
	'-':  true,
	'.':  true,
	'0':  true,
	'1':  true,
	'2':  true,
	'3':  true,
	'4':  true,
	'5':  true,
	'6':  true,
	'7':  true,
	'8':  true,
	'9':  true,
	'A':  true,
	'B':  true,
	'C':  true,
	'D':  true,
	'E':  true,
	'F':  true,
	'G':  true,
	'H':  true,
	'I':  true,
	'J':  true,
	'K':  true,
	'L':  true,
	'M':  true,
	'N':  true,
	'O':  true,
	'P':  true,
	'Q':  true,
	'R':  true,
	'S':  true,
	'T':  true,
	'U':  true,
	'W':  true,
	'V':  true,
	'X':  true,
	'Y':  true,
	'Z':  true,
	'^':  true,
	'_':  true,
	'`':  true,
	'a':  true,
	'b':  true,
	'c':  true,
	'd':  true,
	'e':  true,
	'f':  true,
	'g':  true,
	'h':  true,
	'i':  true,
	'j':  true,
	'k':  true,
	'l':  true,
	'm':  true,
	'n':  true,
	'o':  true,
	'p':  true,
	'q':  true,
	'r':  true,
	's':  true,
	't':  true,
	'u':  true,
	'v':  true,
	'w':  true,
	'x':  true,
	'y':  true,
	'z':  true,
	'|':  true,
	'~':  true,
}

var webSocketKeyMagic = []byte("258EAFA5-E914-47DA-95CA-C5AB0DC85B11")

func computeAcceptKey(challengeKey string) string {
	// Base64 编码后固定28字节
	h := sha1.New()
	h.Write([]byte(challengeKey))
	h.Write(webSocketKeyMagic)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// parseExtensions parses WebSocket extensions from a header.
func parseExtensions(header http.Header) []map[string]string {
	// From RFC 6455:
	//
	//  Sec-WebSocket-Extensions = extension-list
	//  extension-list = 1#extension
	//  extension = extension-token *( ";" extension-param )
	//  extension-token = registered-token
	//  registered-token = token
	//  extension-param = token [ "=" (token | quoted-string) ]
	//     ;When using the quoted-string syntax variant, the value
	//     ;after quoted-string unescaping MUST conform to the
	//     ;'token' ABNF.

	var result []map[string]string
headers:
	for _, s := range header["Sec-Websocket-Extensions"] {
		for {
			var t string
			t, s = nextToken(skipSpace(s))
			if t == "" {
				continue headers
			}
			ext := map[string]string{"": t}
			for {
				s = skipSpace(s)
				if !strings.HasPrefix(s, ";") {
					break
				}
				var k string
				k, s = nextToken(skipSpace(s[1:]))
				if k == "" {
					continue headers
				}
				s = skipSpace(s)
				var v string
				if strings.HasPrefix(s, "=") {
					v, s = nextTokenOrQuoted(skipSpace(s[1:]))
					s = skipSpace(s)
				}
				if s != "" && s[0] != ',' && s[0] != ';' {
					continue headers
				}
				ext[k] = v
			}
			if s != "" && s[0] != ',' {
				continue headers
			}
			result = append(result, ext)
			if s == "" {
				continue headers
			}
			s = s[1:]
		}
	}
	return result
}

// nextTokenOrQuoted returns the leading token or quoted string per RFC 2616
// and the string following the token or quoted string.
func nextTokenOrQuoted(s string) (value string, rest string) {
	if !strings.HasPrefix(s, "\"") {
		return nextToken(s)
	}
	s = s[1:]
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			return s[:i], s[i+1:]
		case '\\':
			p := make([]byte, len(s)-1)
			j := copy(p, s[:i])
			escape := true
			for i = i + 1; i < len(s); i++ {
				b := s[i]
				switch {
				case escape:
					escape = false
					p[j] = b
					j++
				case b == '\\':
					escape = true
				case b == '"':
					return string(p[:j]), s[i+1:]
				default:
					p[j] = b
					j++
				}
			}
			return "", ""
		}
	}
	return "", ""
}

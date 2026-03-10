package kcp

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fixkme/gokit/mlog"
)

const (
	host = ":8283"
)

func TestClient(t *testing.T) {
	hostAddr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		log.Fatal(err)
	}

	startClient := func(id int) {
		port := 33340 + id
		localAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Fatal(err)
		}
		conn, err := net.DialUDP("udp", localAddr, hostAddr)
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		content := fmt.Sprintf("%d handshake request", id)
		if _, err := conn.Write([]byte(content)); err != nil {
			log.Fatal(err)
		}

		cli := &clientImp{conn: conn, kcp: NewKCP(uint32(id), func(buf []byte, size int) {
			if _, err := conn.Write(buf[:size]); err != nil {
				log.Fatal(err)
			}
		})}
		go cli.updateLoop()
		go cli.readLoop()
		cli.Send("hello")
		cli.Send("world")

		time.Sleep(time.Second * 10)
	}

	for i := 0; i < 5; i++ {
		go startClient(i + 1)
	}

	time.Sleep(time.Second * 10)
}

type clientImp struct {
	conn *net.UDPConn
	kcp  *KCP
	mtx  sync.Mutex
}

func (c *clientImp) Send(content string) {
	data := []byte(content)
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if c.kcp.Send(data) < 0 {
		log.Fatal("kcp.Send failed")
	}
	c.kcp.flush(IKCP_FLUSH_FULL)
}

func (c *clientImp) updateLoop() {
	tmer := time.NewTimer(10 * time.Millisecond)
	for {
		<-tmer.C
		c.mtx.Lock()
		c.kcp.Update()
		c.mtx.Unlock()
		tmer.Reset(10 * time.Millisecond)
	}
}

func (c *clientImp) readLoop() {
	buff := make([]byte, 1500)
	recvMsg := func(n int) {
		c.mtx.Lock()
		defer c.mtx.Unlock()
		if r := c.kcp.Input(buff[:n], IKCP_PACKET_REGULAR, true); r < 0 {
			log.Fatal("kcp.Input failed ", r, n)
		}
		if n = c.kcp.Recv(buff); n < 0 {
			fmt.Println("kcp.Recv not ok ", n)
			return
		}
		content := string(buff[:n])
		fmt.Printf("cli recv:%v\n", content)
	}
	for {
		n, err := c.conn.Read(buff)
		if err != nil {
			log.Fatal(err)
		}
		//fmt.Println(buff[:n])
		recvMsg(n)
	}
}

type sessionHandler struct {
	sess *Session
}

func (c *sessionHandler) OnRead(data []byte) bool {
	fmt.Println("server recv:", string(data))
	if err := c.sess.Send(data); err != nil {
		log.Fatal(err)
	}
	return true
}

func (c *sessionHandler) OnConnect(sess *Session) {
	//sess.server.conn.WriteTo([]byte("handshake ok"), sess.remoteAddr)
	c.sess = sess
	sess.Send([]byte("handshake ok"))
}

func (c *sessionHandler) OnClose() {

}

func TestServer(t *testing.T) {
	mlog.UseStdLogger(mlog.DebugLevel)
	opt := &ServerOptions{
		ParseConvId: func(udpPacket []byte) (conv uint32, rest []byte) {
			if len(udpPacket) < 4 {
				return
			}
			conv = binary.LittleEndian.Uint32(udpPacket)
			rest = udpPacket
			return
		},
		OnHandshake: func(udpPacket []byte, raddr net.Addr) (sh SessionHandler, conv uint32, ok bool) {
			content := string(udpPacket)
			if strings.Contains(content, "handshake") {
				vs := strings.Split(content, " ")
				if len(vs) > 0 {
					v, err := strconv.ParseUint(vs[0], 10, 32)
					if err != nil {
						fmt.Println(raddr, "handshake failed")
						return
					}
					conv = uint32(v)
				} else {
					return
				}
				fmt.Println(raddr, "handshake succeed")
				ok = true
				sh = &sessionHandler{}
			}
			return
		},
	}
	server, err := NewServer(host, opt)
	if err != nil {
		t.Fatal(err)
	}
	go server.Run()
	time.Sleep(time.Second * 1000)
}

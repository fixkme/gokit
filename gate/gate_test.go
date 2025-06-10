package gate

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServer(t *testing.T) {
	opt := &ServerOptions{
		Addr: "tcp://127.0.0.1:2333",
	}
	quit := make(chan struct{})
	server := NewServer(opt, newRouterPool(8, 1024, quit))
	server.Run()
}

func TestWsClient(t *testing.T) {
	cliNum := 20
	for i := 0; i < cliNum; i++ {
		go client(i)
	}

	time.Sleep(time.Second * 10)
}

func client(id int) {
	conn, err := net.Dial("tcp", "127.0.0.1:2333")
	if err != nil {
		panic(err)
	}
	key, _ := generateWebSocketKey()
	content := "GET /chat HTTP/1.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n" +
		"x-player-id: " + strconv.Itoa(id) + "\r\n\r\n"
	if _, err = conn.Write([]byte(content)); err != nil {
		panic(err)
	}
	//fmt.Printf("发送握手请求报文完毕\n")
	fmt.Println(string(readHttpReply(conn)))

	n := 5
	for i := 0; i < n; i++ {
		pd := []byte(fmt.Sprintf("%d hello world %d", id, i+1))
		wsh := &WsHead{Fin: true, OpCode: OpBinary, Masked: true, Length: int64(len(pd))}
		geneMask(&wsh.Mask, pd)
		hbf, _ := MakeWsHeadBuff(wsh)
		//fmt.Println(wsh.Mask, ",", string(pd))
		if _, err = conn.Write(hbf); err != nil {
			fmt.Println(err)
			break
		}
		if _, err = conn.Write(pd); err != nil {
			fmt.Println(err)
			break
		}
		fmt.Println(id, "echo:", string(readWsReply(conn)))
		ms := mrand.Intn(100) + 1
		time.Sleep(time.Millisecond * time.Duration(ms))
	}
}

// 客户端生成 WebSocket key
func generateWebSocketKey() (string, error) {
	// 生成 16 字节的随机数
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	// 进行 Base64 编码
	return base64.StdEncoding.EncodeToString(key), nil
}

// 客户端对payload进行掩码
func geneMask(mask *[4]byte, payload []byte) {
	k := (*mask)[:]
	_, err := rand.Read(k)
	if err != nil {
		panic(err)
	}
	MaskWsPayload(*mask, payload)
}

func readHttpReply(c net.Conn) []byte {
	rpy := make([]byte, 1024)
	n, err := c.Read(rpy)
	if err != nil {
		panic(err)
	}
	return rpy[:n]
}

func readWsReply(c net.Conn) []byte {
	wsh, err := ReadWsHeader(c)
	if err != nil {
		panic(err)
	}
	payload := make([]byte, wsh.Length)
	n, err := io.ReadFull(c, payload)
	if err != nil {
		panic(err)
	}
	_ = n
	//fmt.Println("收到字节长度", n)
	return payload
}

type _RoutingTask struct {
	Cli   *_WsClient
	Datas []byte
}
type _RoutingWorkerImp struct {
	tasks chan *_RoutingTask
}

func (r *_RoutingWorkerImp) DoTask(task *_RoutingTask) {
	// 反序列化数据
	// 这里只是用echo作为测试
	//fmt.Printf("routing data %d:%v\n", len(task.Datas), string(task.Datas))
	conn := task.Cli.conn
	conn.Send(task.Datas)
	// 路由数据到game或其他

}

func (r *_RoutingWorkerImp) PushData(session any, datas []byte) {
	r.tasks <- &_RoutingTask{
		Cli:   session.(*_WsClient),
		Datas: datas,
	}
}

func (r *_RoutingWorkerImp) Run(quit chan struct{}) {
	for {
		select {
		case <-quit:
			return
		case task := <-r.tasks:
			r.DoTask(task)
		}
	}
}

type _LoadBalanceImp struct {
	workerSize int
	taskSize   int
	workers    []RoutingWorker
	quit       chan struct{}
	cur        atomic.Uint32
}

func newRouterPool(workerSize, taskSize int, quit chan struct{}) *_LoadBalanceImp {
	p := &_LoadBalanceImp{
		workerSize: workerSize,
		taskSize:   taskSize,
		quit:       quit,
	}
	return p
}

func (p *_LoadBalanceImp) Start() {
	p.workers = make([]RoutingWorker, p.workerSize)
	for i := 0; i < p.workerSize; i++ {
		worker := &_RoutingWorkerImp{
			tasks: make(chan *_RoutingTask, p.taskSize),
		}
		p.workers[i] = worker
		go worker.Run(p.quit)
	}
}

func (p *_LoadBalanceImp) GetOne(cli *_WsClient) RoutingWorker {
	// 轮询
	idx := int(p.cur.Add(1)) % p.workerSize
	return p.workers[idx]
}

func (p *_LoadBalanceImp) OnHandshake(conn *Conn, req *http.Request) error {
	cli := &_WsClient{
		conn:     conn,
		Account:  req.Header.Get("x-account"),
		PlayerId: HttpHeaderGetInt64(req.Header, "x-player-id"),
		ServerId: HttpHeaderGetInt64(req.Header, "x-server-id"),
	}
	conn.BindSession(cli)
	router := p.GetOne(cli)
	conn.BindRoutingWorker(router)
	fmt.Println("Handshake ok ", cli.PlayerId)
	return nil
}

type _WsClient struct {
	conn *Conn

	Account  string
	PlayerId int64
	ServerId int64
}

type _WsClientMgr struct {
	clients map[int64]*_WsClient
	mtx     sync.Mutex
}

var wsClientMgr = &_WsClientMgr{
	clients: make(map[int64]*_WsClient),
}

func (m *_WsClientMgr) Add(client *_WsClient) {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	m.clients[client.PlayerId] = client
}

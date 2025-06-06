package gate

import (
	"fmt"
	"net/http"
)

type RouteTask struct {
	Cli   *WsClient
	Datas []byte
}

type Router interface {
	Run(quit chan struct{})
	PushData(cli *WsClient, datas []byte)
}

type RouterImp struct {
	tasks chan *RouteTask
}

func (r *RouterImp) DoTask(task *RouteTask) {
	// 反序列化数据
	fmt.Printf("rout data %d:%v\n", len(task.Datas), string(task.Datas))
	conn := task.Cli.conn
	conn.Send(task.Datas)
	//fmt.Printf("send echo data:%v\n", task.Datas)
	// 路由数据到game或其他

}

func (r *RouterImp) PushData(cli *WsClient, datas []byte) {
	r.tasks <- &RouteTask{
		Cli:   cli,
		Datas: datas,
	}
}

func (r *RouterImp) Run(quit chan struct{}) {
	for {
		select {
		case <-quit:
			return
		case task := <-r.tasks:
			r.DoTask(task)
		}
	}
}

type RouterPool interface {
	Start(quit chan struct{})
	OnHandshake(conn *Conn, req *http.Request) error
}

type RouterPoolImp struct {
	workerSize int
	taskSize   int
	workers    []Router
}

func NewRouterPool(workerSize, taskSize int) *RouterPoolImp {
	p := &RouterPoolImp{
		workerSize: workerSize,
		taskSize:   taskSize,
	}
	return p
}

func (p *RouterPoolImp) Start(quit chan struct{}) {
	p.workers = make([]Router, p.workerSize)
	for i := 0; i < p.workerSize; i++ {
		worker := &RouterImp{
			tasks: make(chan *RouteTask, p.taskSize),
		}
		p.workers[i] = worker
		go worker.Run(quit)
	}
}

func (p *RouterPoolImp) GetOne(cli *WsClient) Router {
	idx := int(cli.PlayerId) % p.workerSize
	return p.workers[idx]
}

func (p *RouterPoolImp) OnHandshake(conn *Conn, req *http.Request) error {
	cli := &WsClient{
		conn:     conn,
		Account:  req.Header.Get("x-account"),
		PlayerId: HttpHeaderGetInt64(req.Header, "x-player-id"),
		ServerId: HttpHeaderGetInt64(req.Header, "x-server-id"),
	}
	conn.BindClient(cli)
	router := p.GetOne(cli)
	conn.BindRouter(router)
	fmt.Println("Handshake ok ", cli.PlayerId)
	return nil
}

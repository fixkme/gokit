package gate

import (
	"fmt"
	"net/http"
)

type RoutingTask struct {
	Cli   *WsClient
	Datas []byte
}

type RoutingWorker interface {
	Run(quit chan struct{})
	PushData(cli *WsClient, datas []byte)
}

type RoutingWorkerImp struct {
	tasks chan *RoutingTask
}

func (r *RoutingWorkerImp) DoTask(task *RoutingTask) {
	// 反序列化数据
	fmt.Printf("rout data %d:%v\n", len(task.Datas), string(task.Datas))
	conn := task.Cli.conn
	conn.Send(task.Datas)
	//fmt.Printf("send echo data:%v\n", task.Datas)
	// 路由数据到game或其他

}

func (r *RoutingWorkerImp) PushData(cli *WsClient, datas []byte) {
	r.tasks <- &RoutingTask{
		Cli:   cli,
		Datas: datas,
	}
}

func (r *RoutingWorkerImp) Run(quit chan struct{}) {
	for {
		select {
		case <-quit:
			return
		case task := <-r.tasks:
			r.DoTask(task)
		}
	}
}

type LoadBalance interface {
	Start()
	OnHandshake(conn *Conn, req *http.Request) error
}

type LoadBalanceImp struct {
	workerSize int
	taskSize   int
	workers    []RoutingWorker
	quit       chan struct{}
}

func NewRouterPool(workerSize, taskSize int, quit chan struct{}) *LoadBalanceImp {
	p := &LoadBalanceImp{
		workerSize: workerSize,
		taskSize:   taskSize,
		quit:       quit,
	}
	return p
}

func (p *LoadBalanceImp) Start() {
	p.workers = make([]RoutingWorker, p.workerSize)
	for i := 0; i < p.workerSize; i++ {
		worker := &RoutingWorkerImp{
			tasks: make(chan *RoutingTask, p.taskSize),
		}
		p.workers[i] = worker
		go worker.Run(p.quit)
	}
}

func (p *LoadBalanceImp) GetOne(cli *WsClient) RoutingWorker {
	idx := int(cli.PlayerId) % p.workerSize
	return p.workers[idx]
}

func (p *LoadBalanceImp) OnHandshake(conn *Conn, req *http.Request) error {
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

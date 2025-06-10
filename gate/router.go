package gate

import (
	"net/http"
)

type RoutingWorker interface {
	Run(quit chan struct{})
	PushData(session any, datas []byte)
}

type LoadBalance interface {
	Start()
	OnHandshake(conn *Conn, req *http.Request) error
}

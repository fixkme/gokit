package wsg

type RoutingWorker interface {
	PushData(session any, datas []byte)
}

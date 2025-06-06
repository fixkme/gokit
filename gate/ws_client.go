package gate

import "sync"

type WsClient struct {
	conn *Conn

	Account  string
	PlayerId int64
	ServerId int64
}

type WsClientMgr struct {
	clients map[int64]*WsClient
	mtx     sync.Mutex
}

var wsClientMgr = &WsClientMgr{
	clients: make(map[int64]*WsClient),
}

func (m *WsClientMgr) Add(client *WsClient) {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	m.clients[client.PlayerId] = client
}

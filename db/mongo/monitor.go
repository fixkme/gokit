package mongo

import (
	"sync/atomic"

	"go.mongodb.org/mongo-driver/event"
)

type MongoPoolMonitor struct {
	activeConnections int32 // 当前活跃连接数
}

func (p *MongoPoolMonitor) Event(evt *event.PoolEvent) {
	switch evt.Type {
	case event.ConnectionCreated:
		atomic.AddInt32(&p.activeConnections, 1)
	case event.ConnectionClosed:
		atomic.AddInt32(&p.activeConnections, -1)
	}
}

func (p *MongoPoolMonitor) GetActiveConnections() int {
	return int(atomic.LoadInt32(&p.activeConnections))
}

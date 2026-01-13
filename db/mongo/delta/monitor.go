package delta

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"github.com/fixkme/gokit/mlog"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type IDeltaMonitor[ID idType] interface {
	OnCollect(IDeltaCollector[ID])
}

type deltaEntity[ID idType] struct {
	id   ID
	data any
}

type DeltaMonitor[ID idType] struct {
	dBName   string
	collName string
	coll     *mongo.Collection
	changes  map[ID]IDeltaCollector[ID]
	deltas   chan *deltaEntity[ID]
	wg       *sync.WaitGroup
	cancel   context.CancelFunc
}

func NewDeltaMonitor[ID idType](client *mongo.Client, dbName, collName string) (m *DeltaMonitor[ID]) {
	coll := client.Database(dbName).Collection(collName)
	return &DeltaMonitor[ID]{
		dBName:   dbName,
		collName: collName,
		coll:     coll,
		changes:  make(map[ID]IDeltaCollector[ID]),
	}
}

func (m *DeltaMonitor[ID]) Start(pctx context.Context) {
	m.deltas = make(chan *deltaEntity[ID], 1024)
	ctx, cancel := context.WithCancel(pctx)
	m.cancel = cancel
	m.wg = &sync.WaitGroup{}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-ctx.Done():
				for item := range m.deltas {
					m.processEntity(item.id, item.data)
				}
				return
			case item, ok := <-m.deltas:
				if ok {
					m.mustProcessEntity(item.id, item.data)
				}
			}
		}
	}()
}

func (m *DeltaMonitor[ID]) Stop() {
	close(m.deltas)
	m.cancel()
	m.wg.Wait()
	for id, v := range m.changes {
		delta := v.CatchDeltaData()
		m.saveEntityToDb(id, delta)
	}
}

func (m *DeltaMonitor[ID]) PutEntity(id ID, data any, must bool) bool {
	if data == nil {
		return true
	}
	item := &deltaEntity[ID]{id: id, data: data}
	select {
	case m.deltas <- item:
		return true
	default:
		// channel is full
		mlog.Infof("DeltaMonitor PutEntity channel is full, entity id %v, data: %v", id, data)
		if must {
			m.deltas <- item
			return true
		}
		return false
	}
}

func (m *DeltaMonitor[ID]) OnCollect(c IDeltaCollector[ID]) {
	m.changes[c.GetEntityId()] = c
}

func (m *DeltaMonitor[ID]) SaveChangedDatas() {
	for _, v := range m.changes {
		mlog.Debugf("collector size %s:%v", m.collName, v.CollectSize())
		m.saveEntity(v, false)
	}
}

func (m *DeltaMonitor[ID]) SaveEntity(c IDeltaCollector[ID]) {
	m.saveEntity(c, true)
}

func (m *DeltaMonitor[ID]) saveEntity(c IDeltaCollector[ID], must bool) {
	id := c.GetEntityId()
	delta := c.CatchDeltaData()
	if !m.PutEntity(id, delta, must) {
		return
	}
	c.Reset()
	c.SetDataChange(false)
	delete(m.changes, id)
}

const deleteEntityFlag = "DELETE"

func (m *DeltaMonitor[ID]) DeleteEntity(c IDeltaCollector[ID]) {
	id := c.GetEntityId()
	c.SetDelete(true)
	c.SetDataChange(false)
	delete(m.changes, id)
	m.PutEntity(id, deleteEntityFlag, true)
}

const timeout = 5 * time.Second

func (m *DeltaMonitor[ID]) saveEntityToDb(id ID, data any) error {
	defer func() {
		if err := recover(); err != nil {
			mlog.Errorf("save data to mongo panic, entity id %v, data:%v, err:%v\n%v", id, data, err, string(debug.Stack()))
		}
	}()
	if data == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	opts := options.Update().SetUpsert(true)
	filter := bson.M{"_id": id}
	if _, err := m.coll.UpdateOne(ctx, filter, data, opts); err != nil {
		mlog.Errorf("save data to mongo failed, entity id %v, data:%v, err:%v", id, data, err)
		return err
	}
	mlog.Debugf("save data to mongo success, entity id %v, data:%v", id, data)
	return nil
}

func (m *DeltaMonitor[ID]) deleteEntityInDb(id ID) error {
	defer func() {
		if err := recover(); err != nil {
			mlog.Errorf("delete data to mongo panic, entity id %v, err:%v\n%v", id, err, string(debug.Stack()))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	filter := bson.M{"_id": id}
	if _, err := m.coll.DeleteOne(ctx, filter); err != nil {
		mlog.Errorf("delete data to mongo failed, entity id %v, err:%v", id, err)
		return err
	}
	mlog.Debugf("delete data to mongo success, entity id %v", id)
	return nil
}

func (m *DeltaMonitor[ID]) processEntity(id ID, data any) error {
	if data == nil {
		return nil
	}
	switch data {
	case deleteEntityFlag:
		return m.deleteEntityInDb(id)
	default:
		return m.saveEntityToDb(id, data)
	}
}

func (m *DeltaMonitor[ID]) mustProcessEntity(id ID, data any) {
	const retryInterval = 10
	for {
		err := m.processEntity(id, data)
		if err == nil {
			return
		}
		if mongo.IsTimeout(err) || mongo.IsNetworkError(err) || err == mongo.ErrClientDisconnected {
			mlog.Infof("DeltaMonitor (%s, %s) retry save data to mongo after %d seconds", m.dBName, m.collName, retryInterval)
			time.Sleep(retryInterval * time.Second)
		} else {
			return
		}
	}
}

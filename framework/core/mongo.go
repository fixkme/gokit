package core

import (
	"context"
	"time"

	mdb "github.com/fixkme/gokit/db/mongo"
	"github.com/fixkme/gokit/mlog"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var Mongo *MongoImpl

type MongoImpl struct {
	client  *mongo.Client
	monitor *mdb.MongoPoolMonitor
}

type MongoInitOption struct {
	ReadPref     *readpref.ReadPref
	MinPoolSize  uint64
	MaxPoolSize  uint64
	ConnIdleTime time.Duration
}

func InitMongo(uri string, initOptions ...MongoInitOption) error {
	var initOption MongoInitOption
	if len(initOptions) > 0 {
		initOption = initOptions[0]
	} else {
		initOption.MinPoolSize = 0
		initOption.MaxPoolSize = 100
		initOption.ConnIdleTime = 30 * time.Second
	}
	if initOption.ReadPref == nil {
		initOption.ReadPref = readpref.Primary()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	opts := options.Client()
	opts.ApplyURI(uri).
		SetReadPreference(initOption.ReadPref).
		SetBSONOptions(&options.BSONOptions{
			UseJSONStructTags: true,
			NilMapAsEmpty:     true,
			NilSliceAsEmpty:   true,
		})

	if size := initOption.MaxPoolSize; size > 0 {
		opts.SetMaxPoolSize(size)
	}
	if size := initOption.MinPoolSize; size > 0 {
		opts.SetMinPoolSize(size)
	}
	if idleTime := initOption.ConnIdleTime; idleTime > 0 {
		opts.SetMaxConnIdleTime(idleTime)
	}

	monitor := &mdb.MongoPoolMonitor{}
	opts.SetPoolMonitor(&event.PoolMonitor{
		Event: monitor.Event,
	})

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		mlog.Errorf("mongo connect failed: %v, uri: %s", err, uri)
		return err
	}
	if err = client.Ping(ctx, nil); err != nil {
		mlog.Errorf("mongo ping failed: %v, uri: %s", err, uri)
		return err
	}

	Mongo = &MongoImpl{
		client:  client,
		monitor: monitor,
	}
	mlog.Infof("mongo connect success, uri: %s", uri)
	return nil
}

func (m *MongoImpl) Client() *mongo.Client {
	return m.client
}

func (m *MongoImpl) ReadClient() *mongo.Client {
	return m.client
}

func (m *MongoImpl) WriteClient() *mongo.Client {
	return m.client
}

func (m *MongoImpl) GetActiveConnections() int {
	return m.monitor.GetActiveConnections()
}

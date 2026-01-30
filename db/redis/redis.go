package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/fixkme/gokit/mlog"
	"github.com/redis/go-redis/v9"
)

const (
	RedisMode_Single   = "single"
	RedisMode_Sentinel = "sentinel"
	RedisMode_Cluster  = "cluster"
)

type RedisImpl struct {
	client  *redis.Client
	cluster *redis.ClusterClient
}

func NewRedis(mode string, opts any) (*RedisImpl, error) {
	var err error
	db := &RedisImpl{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	switch mode {
	case RedisMode_Cluster:
		db.cluster = redis.NewClusterClient(opts.(*redis.ClusterOptions))
		err = db.cluster.Ping(ctx).Err()
	case RedisMode_Sentinel:
		db.client = redis.NewFailoverClient(opts.(*redis.FailoverOptions))
		err = db.client.Ping(ctx).Err()
	default: // 默认single模式
		db.client = redis.NewClient(opts.(*redis.Options))
		err = db.client.Ping(ctx).Err()
	}

	if err != nil {
		return nil, err
	}

	return db, nil
}

func (db *RedisImpl) Client() *redis.Client {
	return db.client
}

func (db *RedisImpl) ReadClient() *redis.Client {
	return db.client
}

func (db *RedisImpl) WriteClient() *redis.Client {
	return db.client
}

func (db *RedisImpl) ClusterClient() *redis.ClusterClient {
	return db.cluster
}

func (db *RedisImpl) Stop() {
	if db.client != nil {
		db.client.Close()
	}
	if db.cluster != nil {
		db.cluster.Close()
	}
}

func (db *RedisImpl) GetCmdable() redis.Cmdable {
	if db.client != nil {
		return db.client
	}
	if db.cluster != nil {
		return db.cluster
	}
	return nil
}

// PubsubCB 收到redis订阅消息的回调函数
type PubsubCB func(message *redis.Message, db *RedisImpl, cachePtr interface{}) error

// LoadCacheCB (重新)加载缓存的回调函数
type LoadCacheCB func(db *RedisImpl, cachePtr interface{}) error

// Pubsub redis创建"订阅指定格式channel"的PubSub，在新的routinue不断收到订阅消息，并执行回调函数cb
// pubsub接收到error时，如果stop=false，短暂休眠后继续工作；如果stop=true，当前工作routine立即退出
// pattern redis订阅的频道pattern
// cachePtr 指向缓存的指针
// cb 收到订阅消息的回调函数
// loadCB 加载缓存的回调函数
func Pubsub(ctx context.Context, pattern string, stop *bool, db *RedisImpl, cachePtr interface{}, cb PubsubCB, loadCB LoadCacheCB) (*redis.PubSub, error) {
	pubsub, err := subscribe(ctx, pattern, db)
	if err != nil {
		return nil, err
	}

	retryDur := 5 * time.Second
	go func(lstop *bool, lpubsub *redis.PubSub, lcachePtr interface{}) {
		defer func() {
			if r := recover(); r != nil {
				mlog.Errorf("redis Pubsub routinue recover error %v\n", r)
			}
			mlog.Info("redis pubsub routinue quited")
		}()
		for {
			received, err := lpubsub.Receive(ctx)
			if err != nil {
				if *lstop {
					mlog.Infof("redis pubsub stopped on error %v, quit now", err)
					return
				}
				mlog.Infof("redis pubsub error %v, retry after %v", err, retryDur)
				time.Sleep(retryDur)
				continue
			}
			switch v := received.(type) {
			case *redis.Message:
				if err := cb(v, db, lcachePtr); err != nil {
					mlog.Infof("Pubsub onTaskSubscribe %s error %s", v.Channel, err)
				}
			case *redis.Subscription:
				if err := loadCB(db, cachePtr); err != nil {
					mlog.Infof("pubsub loadCB error %s", err)
				}
			case *redis.Pong:
				mlog.Info("pubsub recv Pong")
			default:
				mlog.Infof("pubsub recv default %#v", v)
			}
		}
	}(stop, pubsub, cachePtr)
	return pubsub, nil
}

// 订阅redis消息
func subscribe(ctx context.Context, channel string, db *RedisImpl) (*redis.PubSub, error) {
	var pubsub *redis.PubSub
	if db.client != nil {
		pubsub = db.client.PSubscribe(ctx, channel)
	} else if db.cluster != nil {
		pubsub = db.cluster.PSubscribe(ctx, channel)
	}
	if pubsub == nil {
		return nil, fmt.Errorf("redis subscribe %s failed, nil pubsub", channel)
	}
	return pubsub, nil
}

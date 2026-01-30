package core

import (
	"errors"
	"fmt"
	"strings"

	rdb "github.com/fixkme/gokit/db/redis"
	"github.com/fixkme/gokit/framework/config"
	"github.com/redis/go-redis/v9"
)

var Redis *rdb.RedisImpl

func InitRedis(conf *config.RedisConfig) (err error) {
	if conf == nil {
		return errors.New("redis config is nil")
	}
	addrs := strings.Split(conf.RedisAddr, ",")
	if len(addrs) < 1 {
		return fmt.Errorf("redis addr invalid (%s)", conf.RedisAddr)
	}
	var opt any
	switch conf.RedisMode {
	case rdb.RedisMode_Cluster:
		opt = &redis.ClusterOptions{
			Addrs:    addrs,
			Password: conf.RedisPassword,
		}
	case rdb.RedisMode_Sentinel:
		opt = &redis.FailoverOptions{
			MasterName:    conf.RedisMasterName,
			SentinelAddrs: addrs,
			Password:      conf.RedisPassword,
			DB:            conf.RedisDB,
		}
	default:
		opt = &redis.Options{
			Addr:     addrs[0],
			Password: conf.RedisPassword,
			DB:       conf.RedisDB,
		}
	}
	Redis, err = rdb.NewRedis(conf.RedisMode, opt)
	return
}

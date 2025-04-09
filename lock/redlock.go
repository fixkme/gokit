package lock

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/xid"
)

var (
	ErrFailedLock = errors.New("failed to acquire lock")
)

type RedLock struct {
	rdb    redis.Cmdable
	entity string //请求锁的唯一实例
}

func NewRedLock(rdb redis.Cmdable, entity string) *RedLock {
	return &RedLock{rdb: rdb, entity: entity}
}

// lockKey:分布式锁, expiry:锁超时时间，checkInterval：检测锁的频率
func (l *RedLock) Lock(lockKey string, expiry time.Duration, checkInterval time.Duration) error {
	lockTries := int(expiry/checkInterval) + 1
	for i := 0; i < lockTries; i++ {
		if i != 0 {
			<-time.After(checkInterval)
		}
		ret := l.rdb.SetNX(context.Background(), lockKey, l.entity, expiry)
		if ret.Err() != nil {
			return ret.Err()
		}
		if ret.Val() {
			return nil
		}
	}
	return ErrFailedLock
}

// lockKey:分布式锁，expiry:锁超时时间
func (l *RedLock) TryLock(lockKey string, expiry time.Duration) bool {
	val, err := l.rdb.SetNX(context.Background(), lockKey, l.entity, expiry).Result()
	if err != nil {
		return false
	}
	return val
}

const (
	unlockScriptLua = `
		if redis.call("get",KEYS[1]) == ARGV[1] then
			return redis.call("del",KEYS[1])
		else
			return 0
		end
	`
)

var unlockScript *redis.Script

// lockKey:分布式锁，entity:请求锁的唯一实例
func (l *RedLock) UnLock(lockKey string) (bool, error) {
	if unlockScript == nil {
		unlockScript = redis.NewScript(unlockScriptLua)
	}
	val, err := unlockScript.Run(context.Background(), l.rdb, []string{lockKey}, l.entity).Result()
	if err != nil {
		return false, err
	}
	num, ok := val.(int64)
	if !ok {
		return false, errors.New("ret.Val.(int64) not ok")
	}
	return num != 0, nil
}

// 生成请求锁的唯一实例
func GeneLockEntity() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GeneLockEntity rand.Read err:%v", err)
		return xid.New().String()
	}
	return base64.StdEncoding.EncodeToString(b)
}

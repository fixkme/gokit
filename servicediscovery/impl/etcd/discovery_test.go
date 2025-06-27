package etcd

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/fixkme/gokit/log"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestEtcd(t *testing.T) {
	log.UseDefaultLogger(context.Background(), &sync.WaitGroup{}, "", "mlog", "debug", true)
	ctx, cancel := context.WithTimeout(context.Background(), 30000*time.Second)
	defer cancel()
	conf := &EtcdOpt{
		Config: clientv3.Config{
			Endpoints:            []string{"127.0.0.1:2379"},
			DialTimeout:          5 * time.Second,
			DialKeepAliveTime:    5 * time.Second,
			DialKeepAliveTimeout: 3 * time.Second,
			AutoSyncInterval:     5 * time.Second,
		},
	}
	etcd, err := NewEtcdDiscovery(ctx, "gbs", conf)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	errch := etcd.Start()
	log.Info("etcd started")
	etcd.RegisterService("myself", "127.0.0.1:8080")
	select {
	case err := <-errch:
		log.Error("etcd stopped %v", err)
	}
}

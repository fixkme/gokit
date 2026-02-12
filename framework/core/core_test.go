package core

import (
	"testing"

	"github.com/fixkme/gokit/framework/app"
	"github.com/fixkme/gokit/framework/config"
	"github.com/fixkme/gokit/httpapi"
	"github.com/fixkme/gokit/mlog"
)

func TestHttpApi(t *testing.T) {
	mlog.UseStdLogger(mlog.DebugLevel)
	rpcConf := &config.RpcConfig{
		EtcdEndpoints:   "127.0.0.1:2379",
		RpcGroup:        "test",
		RpcListenAddr:   ":0",
		RpcReadTimeout:  1,
		RpcWriteTimeout: 1,
	}
	if err := InitRpcModule("rpc", nil, rpcConf); err != nil {
		mlog.Fatalf("init rpc failed: %v", err)
	}
	httpApiConf := &config.HttpApiConfig{
		ApiVersion:    "v1",
		ApiListenAddr: ":9091",
	}
	if err := InitHttpApiModule("httpapi", httpApiConf, httpapi.MakeMessageFunc, nil); err != nil {
		mlog.Fatalf("init httpapi failed: %v", err)
	}
	app.DefaultApp().Run(
		Rpc,
		HttpApi,
	)
}

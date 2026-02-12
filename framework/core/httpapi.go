package core

import (
	"fmt"

	"github.com/fixkme/gokit/framework/config"
	"github.com/fixkme/gokit/httpapi"
	"github.com/gin-gonic/gin"
)

var HttpApi *HttpApiModule

type HttpApiModule struct {
	router *httpapi.Server
	conf   *config.HttpApiConfig
	opt    *httpapi.Options
	name   string
}

func InitHttpApiModule(name string, conf *config.HttpApiConfig, makeMsg httpapi.MakeMessageHandler, middlewares []gin.HandlerFunc) error {
	opt := &httpapi.Options{
		ApiVersion:  conf.ApiVersion,
		Middlewares: middlewares,
		MakeMessage: makeMsg,
	}
	HttpApi = &HttpApiModule{
		conf: conf,
		opt:  opt,
		name: name,
	}
	return nil
}

func (s *HttpApiModule) OnInit() error {
	// 确保 Rpc 在前面已经初始化
	if Rpc == nil {
		return fmt.Errorf("RpcModule is nil")
	}
	router, err := httpapi.NewWeb("tcp", s.conf.ApiListenAddr, Rpc, s.opt)
	if err != nil {
		return err
	}
	s.router = router
	return nil
}

func (s *HttpApiModule) Run() {
	if err := s.router.Run(); err != nil {
		err = fmt.Errorf("HttpApi.Run err:%v", err)
		panic(err)
	}
}

func (s *HttpApiModule) Destroy() {
	s.router.Stop()
}

func (s *HttpApiModule) Name() string {
	return "HttpApiServer"
}

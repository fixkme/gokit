package core

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cloudwego/netpoll"
	"github.com/fixkme/gokit/framework/config"
	"github.com/fixkme/gokit/mlog"
	"github.com/fixkme/gokit/rpc"
	"github.com/fixkme/gokit/servicediscovery/impl/etcd"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/proto"
)

var (
	Rpc *RpcModule
)

type RpcModule struct {
	rpcConfig *config.RpcConfig
	serverOpt *rpc.ServerOptions
	cliOpt    *rpc.ClientOptions
	etcdOpt   *etcd.EtcdOptions
	rpcer     *rpc.RpcImp
	name      string
}

func InitRpcModule(name string, handlerFunc rpc.RpcHandler, conf *config.RpcConfig) error {
	if handlerFunc == nil {
		handlerFunc = RpcHandlerFunc
	}
	rpcAddr := conf.RpcAddr
	listenAddr := conf.RpcListenAddr
	if listenAddr == "" && rpcAddr != "" {
		idx := strings.LastIndex(rpcAddr, ":")
		if idx != -1 {
			listenAddr = rpcAddr[idx:]
		}
	}
	pollerOpts := []netpoll.Option{
		netpoll.WithReadTimeout(time.Duration(conf.RpcReadTimeout) * time.Millisecond),   // zero is no effect
		netpoll.WithWriteTimeout(time.Duration(conf.RpcWriteTimeout) * time.Millisecond), // zero is no effect
	}
	pollerNum := conf.RpcPollerNum
	if pollerNum == 0 {
		pollerNum = max(runtime.NumCPU()/2, 1)
	}
	serverOpt := &rpc.ServerOptions{
		ListenAddr:     listenAddr,
		PollerNum:      pollerNum,
		PollOpts:       pollerOpts,
		HandlerFunc:    handlerFunc,
		MsgUnmarshaler: MsgUnmarshaler,
		MsgMarshaler:   MsgMarshaler,
	}
	etcdAddrs := conf.EtcdEndpoints
	etcdOpt := &etcd.EtcdOptions{
		LeaseTTL: conf.EtcdLeaseTTL,
		Config: clientv3.Config{
			Endpoints:            strings.Split(etcdAddrs, ","),
			DialTimeout:          5 * time.Second,
			DialKeepAliveTime:    5 * time.Second,
			DialKeepAliveTimeout: 3 * time.Second,
			AutoSyncInterval:     15 * time.Second,
		},
	}
	clientOpt := &rpc.ClientOptions{
		DailTimeout:  3 * time.Second,
		ReadTimeout:  time.Duration(conf.RpcReadTimeout) * time.Millisecond,
		WriteTimeout: time.Duration(conf.RpcWriteTimeout) * time.Millisecond,
		OnClientClose: func(c netpoll.Connection) error {
			mlog.Infof("%s rpc client conn is closed", c.RemoteAddr().String())
			return nil
		},
		MsgMarshaler:   MsgMarshaler,
		MsgUnmarshaler: MsgUnmarshaler,
	}
	Rpc = &RpcModule{
		rpcConfig: conf,
		serverOpt: serverOpt,
		cliOpt:    clientOpt,
		etcdOpt:   etcdOpt,
		name:      name,
	}
	return nil
}

func (m *RpcModule) Call(serviceName string, cb rpc.RPCReq) (proto.Message, error) {
	return m.rpcer.Call(serviceName, cb)
}

func (m *RpcModule) SyncCall(serviceName string, req, resp proto.Message, timeout time.Duration, md ...*rpc.Meta) (err error) {
	_, err = m.rpcer.Call(serviceName, func(ctx context.Context, cc *rpc.ClientConn) (proto.Message, error) {
		err = rpc.SyncCall(ctx, cc, req, resp, timeout, md...)
		return nil, err
	})
	return
}

func (m *RpcModule) AsyncCallWithoutResp(serviceName string, req proto.Message, md ...*rpc.Meta) (err error) {
	_, err = m.rpcer.Call(serviceName, func(ctx context.Context, cc *rpc.ClientConn) (proto.Message, error) {
		err = rpc.AsyncCallWithoutResp(ctx, cc, req, md...)
		return nil, err
	})
	return
}

func (m *RpcModule) RegisterService(serviceName string, cb func(rpcSrv rpc.ServiceRegistrar, nodeName string) error) error {
	return m.rpcer.RegisterService(serviceName, false, cb)
}

func (m *RpcModule) RegisterServiceOnlyOne(serviceName string, cb func(rpcSrv rpc.ServiceRegistrar, nodeName string) error) error {
	return m.rpcer.RegisterService(serviceName, true, cb)
}

func (m *RpcModule) UnregisterService(serviceName string) error {
	return m.rpcer.UnregisterService(serviceName)
}

func (m *RpcModule) OnInit() error {
	rpcTmp, err := rpc.NewRpc(context.Background(), m.rpcConfig.RpcAddr, m.rpcConfig.RpcGroup, m.etcdOpt, m.serverOpt, m.cliOpt)
	if err != nil {
		return err
	}
	m.rpcer = rpcTmp
	return nil
}

func (m *RpcModule) Run() {
	if err := m.rpcer.Run(); err != nil {
		panic(err)
	}
}

func (m *RpcModule) Destroy() {
	err := m.rpcer.Stop()
	if err != nil {
		mlog.Errorf("%v module stop error: %v", m.name, err)
	}
}

func (m *RpcModule) Name() string {
	return m.name
}

var (
	// 全局默认，用户层pb消息解码
	MsgUnmarshaler = proto.UnmarshalOptions{
		Merge:          false,
		AllowPartial:   true,
		DiscardUnknown: false,
		RecursionLimit: 100,
		NoLazyDecoding: true,
	}
	// 全局默认，用户层pb消息编码
	MsgMarshaler = proto.MarshalOptions{
		AllowPartial:  true,
		Deterministic: false,
	}
)

// 默认RpcHandler
func RpcHandlerFunc(rc *rpc.RpcContext) {
	defer func() {
		if err := recover(); err != nil {
			mlog.Errorf("rpc handler panic, %s, %v\n%s", rc.MethodName, err, debug.Stack())
			rc.ReplyErr = errors.New("rpc handler exception")
		}
		rc.SerializeResponse()
	}()
	argMsg, logicHandler := rc.ReqMsg, rc.Handler
	rc.Reply, rc.ReplyErr = logicHandler(context.Background(), argMsg)
	if rc.ReplyErr == nil {
		mlog.Debugf("rpc handler msg succeed, method:%s, req_data:{%v}, rsp_data:{%v}", rc.MethodName, argMsg, rc.Reply)
	} else {
		mlog.Errorf("rpc handler msg failed, method:%s, req_data:{%v}, err:%v", rc.MethodName, argMsg, rc.ReplyErr)
	}
}

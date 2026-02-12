package rpc

import (
	"context"
	"errors"
	sync "sync"
	"time"

	"github.com/fixkme/gokit/mlog"
	sd "github.com/fixkme/gokit/servicediscovery/discovery"
	"github.com/fixkme/gokit/servicediscovery/impl/etcd"
	"google.golang.org/protobuf/proto"
)

type RpcImp struct {
	etcd sd.Discovery

	rpcAddr string
	server  *Server

	cliOpt  *ClientOptions
	clients map[string]*ClientConn
	cliMtx  sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
}

func NewRpc(pctx context.Context, rpcAddr, serviceGroup string, etcdConf *etcd.EtcdOptions, serverOpt *ServerOptions, cliOpt *ClientOptions) (*RpcImp, error) {
	if rpcAddr == "" && serverOpt.ListenAddr == "" {
		return nil, errors.New("rpcAddr is empty")
	}

	ctx, cancel := context.WithCancel(pctx)
	etcd, err := etcd.NewEtcdDiscovery(ctx, serviceGroup, etcdConf)
	if err != nil {
		cancel()
		return nil, err
	}
	server, err := NewServer(serverOpt, ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	if rpcAddr == "" {
		rpcAddr = server.listener.Addr().String()
		mlog.Infof("peer connect rpc server by addr:%s", rpcAddr)
	}
	imp := &RpcImp{
		etcd:    etcd,
		rpcAddr: rpcAddr,
		server:  server,
		cliOpt:  cliOpt,
		clients: make(map[string]*ClientConn),
		ctx:     ctx,
		cancel:  cancel,
	}
	return imp, nil
}

func (imp *RpcImp) Run() error {
	wg := &sync.WaitGroup{}
	errCh := imp.etcd.Start(wg)
	go func() {
		err := <-errCh
		if err != nil {
			mlog.Errorf("RpcImp etcd error: %v", err)
			imp.cancel()
			imp.server.Stop(context.Background())
		}
		mlog.Info("RpcImp etcd stop")
	}()
	if err := imp.server.Run(); err != nil {
		imp.cancel()
		return err
	}
	wg.Wait()
	mlog.Info("RpcImp Run exit")
	return nil
}

func (imp *RpcImp) Stop() error {
	// 关闭etcd
	imp.cancel()
	// 关闭client
	for _, cli := range imp.clients {
		if err := cli.Close(); err != nil {
			mlog.Errorf("RpcImp cli.Close error: %v", err)
		}
	}
	// 停止server
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := imp.server.Stop(ctx); err != nil {
		return err
	}
	mlog.Info("RpcImp Stoped")
	return nil
}

// 向etcd注册服务, unique=true:注册已存在的服务则返回错误
func (imp *RpcImp) RegisterService(serviceName string, unique bool, cb func(rpcSrv ServiceRegistrar, nodeName string) error) error {
	if unique {
		allService, _ := imp.etcd.GetAllService(serviceName)
		if len(allService) > 0 {
			return errors.New("already exists service")
		}
	}
	nodeName, err := imp.etcd.RegisterService(serviceName, imp.rpcAddr)
	if nil != err {
		return err
	}
	return cb(imp.server, nodeName)
}

// 取消注册服务
func (imp *RpcImp) UnregisterService(serviceName string) error {
	return imp.etcd.UnregisterService(serviceName)
}

func (imp *RpcImp) Call(serviceName string, cb RPCReq) (proto.Message, error) {
	addr, err := imp.etcd.GetService(serviceName)
	if err != nil {
		return nil, err
	}

	imp.cliMtx.RLock()
	cli, ok := imp.clients[addr]
	imp.cliMtx.RUnlock()
	if !ok {
		if cli, err = imp.connectTo(addr); nil != err {
			return nil, err
		}
	}
	return cb(imp.ctx, cli)
}

func (imp *RpcImp) CallAll(serviceName string, cb RPCReq) ([]proto.Message, error) {
	addrs, err := imp.etcd.GetAllService(serviceName)
	if err != nil {
		return nil, err
	}
	rsps := []proto.Message{}
	for _, addr := range addrs {
		imp.cliMtx.RLock()
		cli, ok := imp.clients[addr]
		imp.cliMtx.RUnlock()
		if !ok {
			if cli, err = imp.connectTo(addr); nil != err {
				mlog.Errorf("CallAll [%s,%s] connect err:%v", serviceName, addr, err)
				continue
			}
		}
		rsp, err := cb(imp.ctx, cli)
		if err != nil {
			mlog.Errorf("CallAll [%s,%s] cb err:%v", serviceName, addr, err)
		} else {
			rsps = append(rsps, rsp)
		}
	}
	return rsps, nil
}

func (imp *RpcImp) connectTo(rpcAddr string) (*ClientConn, error) {
	cli, err := NewClientConn("tcp", rpcAddr, imp.cliOpt)
	if err != nil {
		return nil, err
	}
	imp.cliMtx.Lock()
	imp.clients[rpcAddr] = cli
	imp.cliMtx.Unlock()
	return cli, nil
}

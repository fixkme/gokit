package rpc

import (
	"context"
	"errors"
	sync "sync"
	"time"

	"github.com/cloudwego/netpoll"
	"github.com/fixkme/gokit/mlog"
	sd "github.com/fixkme/gokit/servicediscovery/discovery"
	"github.com/fixkme/gokit/servicediscovery/impl/etcd"
	"google.golang.org/protobuf/proto"
)

type RPCReq func(context.Context, *ClientConn) (proto.Message, error)

type RpcImp struct {
	etcd sd.Discovery

	rpcAddr string
	server  *Server

	clients map[string]*ClientConn
	cliMtx  sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
}

func NewRpc(pctx context.Context, rpcAddr, serviceGroup string, etcdConf *etcd.EtcdOpt, serverOpt *ServerOpt) (*RpcImp, error) {
	if rpcAddr == "" {
		if rpcAddr = getOneInnerIP(); rpcAddr == "" {
			return nil, errors.New("no inner ip")
		}
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
	imp := &RpcImp{
		etcd:    etcd,
		rpcAddr: rpcAddr,
		server:  server,
		clients: make(map[string]*ClientConn),
		ctx:     ctx,
		cancel:  cancel,
	}
	return imp, nil
}

func (imp *RpcImp) Run() error {
	errCh := imp.etcd.Start()
	go func() {
		err := <-errCh
		if err != nil {
			mlog.Error("RpcImp etcd error: %v", err)
			imp.cancel()
			imp.server.Stop(context.Background())
		}
		mlog.Info("RpcImp etcd stop")
	}()
	if err := imp.server.Run(); err != nil {
		imp.cancel()
		return err
	}
	mlog.Info("RpcImp Run exit")
	return nil
}

func (imp *RpcImp) Stop() error {
	imp.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := imp.server.Stop(ctx); err != nil {
		return err
	}
	mlog.Info("RpcImp Stop")
	return nil
}

// 向etcd注册服务
func (imp *RpcImp) RegisterService(serviceName string, cb func(rpcSrv *Server, nodeName string) error) error {
	nodeName, err := imp.etcd.RegisterService(serviceName, imp.rpcAddr)
	if nil != err {
		return err
	}
	return cb(imp.server, nodeName)
}

// 向etcd注册唯一服务， 已注册相同的服务则返回错误
func (imp *RpcImp) RegisterServiceOnlyOne(serviceName string, cb func(rpcSrv *Server, nodeName string) error) error {
	allService, _ := imp.etcd.GetAllService(serviceName)
	if len(allService) > 0 {
		return errors.New("already exists service")
	}
	nodeName, err := imp.etcd.RegisterService(serviceName, imp.rpcAddr)
	if nil != err {
		return err
	}
	return cb(imp.server, nodeName)
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

func (imp *RpcImp) connectTo(rpcAddr string) (*ClientConn, error) {
	opt := &ClientOpt{
		DailTimeout:    time.Second * 3,
		ShardQueueSize: 4,
		OnClientClose: func(c netpoll.Connection) error {
			mlog.Info("%s rpc client conn closed", c.RemoteAddr().String())
			return nil
		},
	}
	cli, err := NewClientConn("tcp", rpcAddr, opt)
	if err != nil {
		return nil, err
	}
	imp.cliMtx.Lock()
	imp.clients[rpcAddr] = cli
	imp.cliMtx.Unlock()
	return cli, nil
}

package rpc

import (
	"context"
	"io"
	"net"
	"os"
	sync "sync"
	"time"

	"github.com/fixkme/gokit/log"
	sd "github.com/fixkme/gokit/servicediscovery/discovery"
	"github.com/fixkme/gokit/servicediscovery/impl/etcd"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// GRPCReq gRPC请求函数
type GRPCReq = func(context.Context, *grpc.ClientConn) (proto.Message, error)

// GRPCReg gRPC注册函数 serviceFullName注册的服务全名: serviceName:uuid
type GRPCReg = func(serv *grpc.Server, serviceFullName string) error

// GRPCer grpc封装
type GRPCer interface {
	// 客户端调用某个服务
	Call(serviceName string, cb GRPCReq) (proto.Message, error)
	CallAll(serviceName string, cb GRPCReq) ([]proto.Message, error)
	// 注册服务
	Register(serviceName string, cb GRPCReg) error

	// 将grpc跑起来
	Start() <-chan error
	// 停止grpc
	Stop()
}

// NewGRPCer 创建GRPC，参数为grpc监听地址，格式:ip:port
func NewGRPCer(ctx context.Context, grpcAddr string, etcdConf *etcd.EtcdOpt) (GRPCer, error) {
	netListen, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return nil, err
	}
	log.Info("gRPC listen: %s", netListen.Addr())

	etcds, err := etcd.NewEtcdDiscovery(ctx, etcdConf)
	if err != nil {
		return nil, err
	}

	opts := []grpc_recovery.Option{
		grpc_recovery.WithRecoveryHandler(func(p interface{}) (err error) {
			log.Error("gRPC调用出错 panic: %v", p)
			return status.Errorf(1002, "服务器内部错误")
		}),
	}

	grpcServ := grpc.NewServer(
		grpc_middleware.WithUnaryServerChain(
			grpc_recovery.UnaryServerInterceptor(opts...),
		),
		grpc_middleware.WithStreamServerChain(
			grpc_recovery.StreamServerInterceptor(opts...),
		),
		grpc.MaxRecvMsgSize(MaxCommuBuff),
		grpc.MaxSendMsgSize(MaxCommuBuff),
	)

	imp := &grpcerImp{
		grpcGroup: etcdConf.ServiceGroup,
		grpcAddr:  grpcAddr,
		netListen: netListen,
		grpcServ:  grpcServ,
		ctx:       ctx,
		grpcCli:   make(map[string]*grpc.ClientConn, 100),
		etcd:      etcds,
	}
	return imp, nil
}

type grpcerImp struct {
	grpcGroup string          // grpc 组名
	grpcAddr  string          // grpc 服务绑定地址
	netListen net.Listener    // 监听服务
	grpcServ  *grpc.Server    // grpc服务
	ctx       context.Context // 上下文

	grpcCli map[string]*grpc.ClientConn // grpc客户端
	cliMx   sync.RWMutex                // rpcCli 保护变量
	wg      sync.WaitGroup              // 退出一致性

	etcd sd.Discovery // etcd
}

const (
	// ClientKeepaliveTime -> GRPC_ARG_KEEPALIVE_TIME_MS
	ClientKeepaliveTime = 10 * time.Second

	// ClientKeepaliveTimeout -> GRPC_ARG_KEEPALIVE_TIMEOUT_MS
	ClientKeepaliveTimeout = 3 * time.Second

	// ClientTimeout 超时时间
	ClientTimeout = 5 * time.Second

	// MaxCommuBuff 最大通讯缓存
	MaxCommuBuff = 10 * 1024 * 1024
)

func (imp *grpcerImp) Call(serviceName string, cb GRPCReq) (proto.Message, error) {
	addr, err := imp.etcd.GetService(serviceName)
	if err != nil {
		return nil, err
	}

	imp.cliMx.RLock()
	conn, ok := imp.grpcCli[addr]
	imp.cliMx.RUnlock()
	if !ok {
		if conn, err = imp.connectTo(addr); nil != err {
			return nil, err
		}
	}
	return cb(imp.ctx, conn)
}

func (imp *grpcerImp) call(addr string, cb GRPCReq) (proto.Message, error) {
	var err error
	imp.cliMx.RLock()
	conn, ok := imp.grpcCli[addr]
	imp.cliMx.RUnlock()
	if !ok {
		if conn, err = imp.connectTo(addr); nil != err {
			log.Error("call, imp.connectTo err:%v", err)
			return nil, err
		}
	}
	return cb(imp.ctx, conn)
}

// 向所有同类型服务器
func (imp *grpcerImp) CallAll(serviceName string, cb GRPCReq) ([]proto.Message, error) {
	addrs, err := imp.etcd.GetAllService(serviceName)
	if err != nil {
		return nil, err
	}
	rsps := []proto.Message{}
	for _, addr := range addrs {
		p, err := imp.call(addr, cb)
		if nil != err {
			log.Error("CallAll, imp.call err:%v", err)
			// return nil, err
		}
		rsps = append(rsps, p)
	}
	return rsps, nil
}

func (imp *grpcerImp) connectTo(addr string) (*grpc.ClientConn, error) {
	log.Debug("find target %s, dail right now", addr)
	ctx, cancel := context.WithTimeout(imp.ctx, ClientTimeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock(), grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                ClientKeepaliveTime,
		Timeout:             ClientKeepaliveTimeout,
		PermitWithoutStream: true,
	}), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxCommuBuff), grpc.MaxCallSendMsgSize(MaxCommuBuff)))
	if err != nil {
		return nil, err
	}

	imp.cliMx.Lock()
	exsit, ok := imp.grpcCli[addr]
	if ok { // 已经处理过了
		imp.cliMx.Unlock()
		conn.Close()
		return exsit, nil
	}

	imp.grpcCli[addr] = conn
	imp.cliMx.Unlock()

	log.Info("target %s rpc client cached", addr)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("gRPC connectTo recover error %v", r)
			}
		}()
		for {
			if conn.GetState() == connectivity.Shutdown || conn.GetState() == connectivity.TransientFailure {
				log.Info("target %s rpc client %v", addr, conn.GetState())
				imp.cliMx.Lock()
				defer imp.cliMx.Unlock()
				delete(imp.grpcCli, addr)
				conn.Close()
				return
			}
			oldState := conn.GetState()
			conn.WaitForStateChange(imp.ctx, conn.GetState())
			log.Info("grpc conn change,target addr:%v, oldState:%v, newState:%v", addr, oldState, conn.GetState())
		}
	}()

	return conn, nil
}

func (imp *grpcerImp) Register(serviceName string, cb GRPCReg) error {
	// 注册etcd服务
	fullname, err := imp.etcd.RegisterService(serviceName, imp.grpcAddr)
	if nil != err {
		return err
	}
	return cb(imp.grpcServ, fullname)
}

func (imp *grpcerImp) Start() <-chan error {
	// 不调用NewLogger时，默认的gRPC日志没生效，这里强制创建一份自定义logger
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, os.Stderr, os.Stderr))

	errChan := make(chan error, 10)
	imp.wg.Add(1)
	go func() {
		etcdErr := imp.etcd.Start()
		err := <-etcdErr // 有任意错误就返回
		if err != nil {
			errChan <- err
		}
		imp.wg.Done()
	}()

	// grpc server
	imp.wg.Add(1)
	go func() {
		if err := imp.grpcServ.Serve(imp.netListen); err != nil {
			errChan <- err
		}
		imp.wg.Done()
	}()

	// 异步结束
	imp.wg.Add(1)
	go func() {
		defer imp.wg.Done()
		<-imp.ctx.Done()
		imp.grpcServ.GracefulStop()
	}()

	return errChan
}

func (imp *grpcerImp) Stop() {
	imp.etcd.Stop()
	imp.grpcServ.GracefulStop()
	imp.cliMx.Lock()
	for addr, conn := range imp.grpcCli {
		conn.Close()
		log.Info("close addr %v grpc connection", addr)
	}
	imp.cliMx.Unlock()
	imp.wg.Wait()
}

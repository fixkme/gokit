package etcd

import (
	"context"
	"errors"
	"fmt"
	"gokit/log"
	sd "gokit/servicediscovery/discovery"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	// etcd客户端申请有效期为该值的租约，当在该值时间内没有收到keepAlive包，租约将失效(相关的key会被删除)
	defaultTimeToLiveSeconds = 5

	// etcd put事件
	eventType_Put = 0

	// etcd delete事件
	eventType_Delete = 1
)

type EtcdOpt struct {
	Endpoints            []string `json:"endpoints"`
	DialTimeout          int64    `json:"dialTimeout"`
	DialKeepAliveTime    int64    `json:"dialKeepAliveTime"`
	DialKeepAliveTimeout int64    `json:"dialKeepAliveTimeout"`
	AutoSyncInterval     int64    `json:"autoSyncInterval"`
	LeaseTTL             int64    `json:"leaseTTL"`
	ServiceGroup         string   `json:"serviceGroup"`
}

// NewEtcdDiscovery 创建一个etcd实例
func NewEtcdDiscovery(ctx context.Context, opt *EtcdOpt) (sd.Discovery, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints: opt.Endpoints,
		// 注意，设置了DialTimeout参数，clientv3.New会是阻塞call(如果需要非阻塞call，则不能设置DailTimeout参数)
		// 详见 https://github.com/etcd-io/etcd/issues/9829#issuecomment-438434795
		DialTimeout:          time.Duration(opt.DialTimeout) * time.Second,
		DialKeepAliveTime:    time.Duration(opt.DialKeepAliveTime) * time.Second,
		DialKeepAliveTimeout: time.Duration(opt.DialKeepAliveTimeout) * time.Second,
		AutoSyncInterval:     time.Duration(opt.AutoSyncInterval) * time.Second,
	})
	if err != nil {
		return nil, err
	}

	if opt.LeaseTTL == 0 {
		opt.LeaseTTL = defaultTimeToLiveSeconds
	}

	// 监视"service:"前缀的key，此类key变动时，将通知到rch管道
	prefix := fmt.Sprintf("%s:service:", opt.ServiceGroup)
	// 类似于etcdctl watch的效果，etcd client和server维持监视效果
	// server中有相关的key变动，立即通知到client driver，然后将数据发送到rch管道
	rch := cli.Watch(ctx, prefix, clientv3.WithPrefix())
	if rch == nil {
		return nil, fmt.Errorf("watch etcd %v error", opt.Endpoints)
	}

	return &etcdImp{
		cli:         cli,
		prefix:      prefix,
		allServices: make(serviceContainer),
		regServs:    make(map[string]string),
		rch:         rch,
		ctx:         ctx,
		leaseTTL:    opt.LeaseTTL,
	}, nil
}

// serviceSet 一类服务集合，比如Resource服务
type serviceSet struct {
	id2addr map[string]string // UUID -> rpc地址
	ids     []string          // id2addr中的全部key(UUID)，用于随机选择服务地址
}

// serviceContainer 装载各种服务的集合 格式为 key:serviceName value:serviceSet
type serviceContainer map[string]*serviceSet

// etcdImp etcd实例数据结构
type etcdImp struct {
	// cli etcd client
	cli *clientv3.Client

	// etcd中保存的服务前缀，格式：xxx:service
	prefix string
	// 本集群所有services cache
	allServices serviceContainer
	// 自己已注册的服务
	regServs map[string]string

	mx  sync.RWMutex
	ctx context.Context

	// 等待etcd watch返回的管道
	rch clientv3.WatchChan

	// 客户端申请的租约有效期
	leaseTTL int64
}

// Start Start the etcd client service
func (e *etcdImp) Start() <-chan error {
	errChan := make(chan error, 10)
	// 将etcd中已经存在的服务缓存到本地（e.services）
	go e.cacheExistedServices()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("etcd run recover error %v", r)
			}
		}()
		for {
			select {
			case <-e.ctx.Done():
				errChan <- nil
				return
			case watchRsp, ok := <-e.rch:
				if !ok {
					log.Info("etcd watch channel closed!!!")
					errChan <- nil
					return
				}
				if err := watchRsp.Err(); err != nil {
					log.Warn("etcd watch response error: %v", err)
					// TODO watch出错后，重新发起watch，并修改Etcd.rch
					errChan <- err
					return
				}
				for _, evt := range watchRsp.Events {
					if evt != nil {
						e.onWatchEvent(evt)
					}
				}
			}
		}
	}()

	return errChan
}

// Stop 关闭etcd连接
func (e *etcdImp) Stop() {
	if e.cli == nil {
		return
	}

	for k := range e.regServs {
		e.cli.Delete(e.ctx, k) // 停止时，不关注error
	}

	if err := e.cli.Close(); err != nil {
		log.Warn("etcd stop, Close error %v", err)
	}
}

// PutService 发布服务到etcd
func (e *etcdImp) PutService(serviceName string, rpcAddr string) (string, error) {
	serviceName = strings.ToLower(serviceName)
	// 通过UUID，生成新的服务名，确保唯一性
	serviceUUID := uuid.New().String()
	nodeName := fmt.Sprintf("%s:%s", serviceName, serviceUUID)
	key := fmt.Sprintf("%s%s", e.prefix, nodeName)
	if err := e.putServiceKey(key, rpcAddr); err != nil {
		return nodeName, err
	}
	return nodeName, nil
}

// 租约过期删除的key（gbs:service:gate:b748593c-ec50-4b4c-8b4a-21705dd1789f）, 再次注册该key,
func (e *etcdImp) putServiceKey(key string, rpcAddr string) error {
	resp, err := e.cli.Grant(e.ctx, e.leaseTTL)
	if err != nil {
		return err
	}
	log.Info("etcd Grant lease ID: %X, TTL %d", resp.ID, e.leaseTTL)
	_, err = e.cli.Put(e.ctx, key, rpcAddr, clientv3.WithLease(resp.ID))
	if err != nil {
		return err
	}
	log.Info("etcd PUT %s %s", key, rpcAddr)
	e.regServs[key] = rpcAddr
	ch, err := e.cli.KeepAlive(e.ctx, resp.ID)
	if err != nil {
		return err
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("etcd keepalive recover error %v", r)
			}
		}()
		for {
			_, ok := <-ch
			if !ok {
				log.Info("etcd key: %s KeepAlive channel closed", key)
				return
			}
		}
	}()
	return nil
}

func (e *etcdImp) GetService(serviceName string) (string, error) {
	serviceName = strings.ToLower(serviceName)
	var serviceUUID string

	if index := strings.Index(serviceName, ":"); index != -1 {
		serviceName, serviceUUID = serviceName[:index], serviceName[index+1:]
	}

	e.mx.RLock()
	defer e.mx.RUnlock()
	services, ok := e.allServices[serviceName]
	if !ok || len(services.ids) == 0 {
		return "", fmt.Errorf("not found service (%s)", serviceName)
	}
	// 从可用的服务中随机选择一个
	if len(serviceUUID) == 0 {
		index := rand.Intn(len(services.ids))
		serviceUUID = services.ids[index]
	}
	addr, ok := services.id2addr[serviceUUID]
	if !ok {
		log.Debug("GetService %s, rpc addr %s, id2addr %#v", serviceName, addr, services.id2addr)
		return "", fmt.Errorf("%s rpc not found", serviceName)
	}
	return addr, nil
}

func (e *etcdImp) GetAllService(serviceName string) (rpcAddrs map[string]string, err error) {
	serviceName = strings.ToLower(serviceName)

	if index := strings.Index(serviceName, ":"); index != -1 {
		serviceName = serviceName[:index]
	}

	e.mx.RLock()
	defer e.mx.RUnlock()
	services, ok := e.allServices[serviceName]
	if !ok {
		return nil, fmt.Errorf("not found service (%s)", serviceName)
	}
	rpcAddrs = make(map[string]string)
	for id, addr := range services.id2addr {
		rpcAddrs[id] = addr
	}
	return
}

// 处理etcd watch的返回结果
func (e *etcdImp) onWatchEvent(evt *clientv3.Event) {
	key := string(evt.Kv.Key)
	value := string(evt.Kv.Value)
	log.Info("etcd onWatchEvent type %s, key %s, value %s", evt.Type, key, value)

	name, id, err := e.parseKey(key)
	if err != nil {
		log.Error("etcd onWatchEvent parseKey fail, key:%s, err:%v", key, err)
		return
	}

	e.mx.Lock()
	defer e.mx.Unlock()

	if int32(evt.Type) == eventType_Delete { // 为了不引入mvccpb
		if e.delService(name, id) {
			log.Info("etcd onWatchEvent delete (%s,%s)", name, id)
		}
		// 删除的是本节点服务，重新注册此服务（被删除的原因，可能是keepalive超时了）
		if rpcAddr, ok := e.regServs[key]; ok {
			log.Info("etcd OnWatchEvent register again, key:%s, rpcAddr:%s", key, rpcAddr)
			if err := e.putServiceKey(key, rpcAddr); err != nil {
				log.Error("etcd onWatchEvent putServiceKey err:%v", err)
			}
		}
	} else if int32(evt.Type) == eventType_Put {
		if e.addService(name, id, value) {
			log.Info("etcd onWatchEvent addService, %s -> %s", key, value)
		}
	}
}

// 解析key(gbs:service:gate:b748593c-ec50-4b4c-8b4a-21705dd1789f)为 [gate，UUID]
func (e *etcdImp) parseKey(key string) (name, id string, err error) {
	if !strings.HasPrefix(key, e.prefix) {
		err = errors.New("key not match prefix")
		return
	}
	keys := strings.Split(key[len(e.prefix):], ":")
	if len(keys) != 2 {
		err = errors.New("key not match format")
		return
	}
	name, id = keys[0], keys[1]
	return
}

// 添加服务
func (e *etcdImp) addService(name string, id string, addr string) bool {
	services, ok := e.allServices[name]
	if !ok {
		services = &serviceSet{
			id2addr: make(map[string]string),
		}
		e.allServices[name] = services
	}
	if _, ok := services.id2addr[id]; !ok {
		services.id2addr[id] = addr
		services.ids = append(services.ids, id)
		return true
	}
	return false
}

// 删除服务
func (e *etcdImp) delService(name string, id string) bool {
	services, ok := e.allServices[name]
	if !ok {
		return false
	}

	delete(services.id2addr, id)
	for i, v := range services.ids {
		if v == id {
			length := len(services.ids)
			services.ids[i] = services.ids[length-1]
			services.ids = services.ids[:length-1]
			return true
		}
	}
	return false
}

// 将客户端watch之前已经存在于etcd的service缓存下来
func (e *etcdImp) cacheExistedServices() {
	rsp, err := e.cli.Get(e.ctx, e.prefix, clientv3.WithPrefix())
	if err != nil {
		log.Warn("cacheExistedServices Get error %v", err)
		return
	}

	e.mx.Lock()
	defer e.mx.Unlock()

	var key, value string
	for _, v := range rsp.Kvs {
		if v == nil {
			continue
		}
		key = string(v.Key)
		value = string(v.Value)
		if len(key) < len(e.prefix) {
			continue
		}
		if name, id, err := e.parseKey(key); err == nil {
			if e.addService(name, id, value) {
				log.Info("cacheExistedServices addService, %s -> %s", key, value)
			}
		}
	}
}

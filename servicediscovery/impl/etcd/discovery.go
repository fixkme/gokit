package etcd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fixkme/gokit/mlog"
	sd "github.com/fixkme/gokit/servicediscovery/discovery"
	v3rpc "go.etcd.io/etcd/api/v3/v3rpc/rpctypes"

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
	clientv3.Config `json:",inline"`
	LeaseTTL        int64 `json:"leaseTTL"`
}

// NewEtcdDiscovery 创建一个etcd实例
func NewEtcdDiscovery(ctx context.Context, serviceGroup string, opt *EtcdOpt) (sd.Discovery, error) {
	cli, err := clientv3.New(opt.Config)
	if err != nil {
		return nil, err
	}

	if opt.LeaseTTL <= 0 {
		opt.LeaseTTL = defaultTimeToLiveSeconds
	}

	// 监视"service:"前缀的key，此类key变动时，将通知到rch管道
	prefix := fmt.Sprintf("%s:service:", serviceGroup)

	e := &etcdImp{
		cli:         cli,
		prefix:      prefix,
		allServices: make(serviceContainer),
		regServs:    sync.Map{},
		rch:         nil,
		ctx:         ctx,
		leaseTTL:    opt.LeaseTTL,
	}

	//加载所有服务， 然后监听后续数据
	if err := e.loadAndWatch(); err != nil {
		return nil, err
	}
	return e, nil
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
	mx          sync.RWMutex

	// 自己已注册的服务
	regServs sync.Map
	quit     atomic.Bool

	ctx context.Context

	// 等待etcd watch返回的管道
	rch clientv3.WatchChan
	// 客户端申请的租约有效期
	leaseTTL int64
}

// Start Start the etcd client service
func (e *etcdImp) Start(wg *sync.WaitGroup) <-chan error {
	e.quit.Store(false)
	errChan := make(chan error, 1)
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				mlog.Errorf("etcd run panic error: %v", r)
			}
		}()
		defer func() {
			e.onStop()
			if wg != nil {
				wg.Done()
			}
		}()
		for {
			select {
			case <-e.ctx.Done():
				errChan <- nil
				return
			case watchRsp, ok := <-e.rch:
				if !ok {
					mlog.Info("etcd watch channel closed!!!")
					errChan <- errors.New("etcd watch channel closed")
					return
				}
				if err := watchRsp.Err(); err != nil {
					mlog.Warnf("etcd watch response error: %v", err)
					if err = e.processWatchError(err); err != nil {
						errChan <- err
						return
					}
					continue
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
}

func (e *etcdImp) onStop() {
	e.quit.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e.regServs.Range(func(key, value any) bool {
		if _, err := e.cli.Delete(ctx, key.(string)); err != nil {
			mlog.Warnf("etcd stop, Delete key error:%v", err)
		}
		return true
	})

	// cli.Close()会触发e.rch close, 但是通过调用顺序规避了
	if err := e.cli.Close(); err != nil {
		mlog.Warnf("etcd stop, Close error:%v", err)
	}
	mlog.Info("etcd closed")
}

// RegisterService 发布服务到etcd，返回服务唯一标识（gate:b748593c-ec50-4b4c-8b4a-21705dd1789f）
func (e *etcdImp) RegisterService(serviceName string, rpcAddr string) (string, error) {
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
	mlog.Infof("etcd Grant lease ID: %X, TTL %d", resp.ID, e.leaseTTL)
	_, err = e.cli.Put(e.ctx, key, rpcAddr, clientv3.WithLease(resp.ID))
	if err != nil {
		return err
	}
	mlog.Infof("etcd PUT %s %s", key, rpcAddr)
	e.regServs.Store(key, rpcAddr)
	ch, err := e.cli.KeepAlive(e.ctx, resp.ID)
	if err != nil {
		return err
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				mlog.Errorf("etcd keepalive recover error %v", r)
			}
		}()
		for {
			_, ok := <-ch
			if !ok {
				mlog.Infof("etcd key: %s KeepAlive channel closed", key)
				return
			}
		}
	}()
	return nil
}

// UnregisterService 取消注册服务
func (e *etcdImp) UnregisterService(serviceName string) (err error) {
	e.regServs.Range(func(k, v any) bool {
		key := k.(string)
		ss := strings.Split(key[len(e.prefix):], ":")
		if slices.Index(ss, serviceName) >= 0 {
			if _, err = e.cli.Delete(e.ctx, key); err != nil {
				return false
			}
			e.regServs.Delete(key)
		}
		return true
	})
	return nil
}

// GetService 获取服务地址
func (e *etcdImp) GetService(serviceName string) (string, error) {
	serviceName = strings.ToLower(serviceName)
	var serviceUUID string
	if index := strings.Index(serviceName, ":"); index != -1 {
		serviceName, serviceUUID = serviceName[:index], serviceName[index+1:]
	}

	e.mx.RLock()
	defer e.mx.RUnlock()
	services, ok := e.allServices[serviceName]
	if !ok {
		return "", fmt.Errorf("not exist service (%s)", serviceName)
	} else if len(services.ids) == 0 {
		return "", fmt.Errorf("not found service (%s)", serviceName)
	}
	// 从可用的服务中随机选择一个
	if len(serviceUUID) == 0 {
		index := rand.Intn(len(services.ids))
		serviceUUID = services.ids[index]
	}
	addr, ok := services.id2addr[serviceUUID]
	if !ok {
		return "", fmt.Errorf("not found addr of service (%s)", serviceName)
	}
	return addr, nil
}

// GetAllService 获取所有服务地址
func (e *etcdImp) GetAllService(serviceName string) (rpcAddrs map[string]string, err error) {
	serviceName = strings.ToLower(serviceName)
	if index := strings.Index(serviceName, ":"); index != -1 {
		serviceName = serviceName[:index]
	}

	e.mx.RLock()
	defer e.mx.RUnlock()
	services, ok := e.allServices[serviceName]
	if !ok {
		return nil, fmt.Errorf("not exist service (%s)", serviceName)
	}
	rpcAddrs = maps.Clone(services.id2addr)
	return
}

// 处理etcd watch的返回结果
func (e *etcdImp) onWatchEvent(evt *clientv3.Event) {
	key := string(evt.Kv.Key)
	value := string(evt.Kv.Value)
	mlog.Infof("etcd onWatchEvent type %s, key %s, value %s", evt.Type, key, value)

	name, id, err := e.parseKey(key)
	if err != nil {
		mlog.Errorf("etcd onWatchEvent parseKey fail, key:%s, err:%v", key, err)
		return
	}

	e.mx.Lock()
	defer e.mx.Unlock()

	if int32(evt.Type) == eventType_Delete {
		if e.delService(name, id) {
			mlog.Infof("etcd onWatchEvent delete (%s,%s)", name, id)
		}

		// 删除的是本节点服务，重新注册此服务（被删除的原因，可能是keepalive超时了）
		if rpcAddr, ok := e.regServs.Load(key); ok && !e.quit.Load() {
			mlog.Infof("etcd OnWatchEvent register again, key:%s, rpcAddr:%s", key, rpcAddr)
			if err := e.putServiceKey(key, rpcAddr.(string)); err != nil {
				mlog.Errorf("etcd onWatchEvent putServiceKey err:%v", err)
			}
		}
	} else if int32(evt.Type) == eventType_Put {
		if e.addService(name, id, value) {
			mlog.Infof("etcd onWatchEvent addService, %s -> %s", key, value)
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

// 添加发现的服务
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

// 删除下线的服务
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

// 将已经注册于etcd的service缓存load进来
func (e *etcdImp) loadExistedServices() (revision int64, err error) {
	ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancel()
	rsp, err := e.cli.Get(ctx, e.prefix, clientv3.WithPrefix())
	if err != nil {
		mlog.Errorf("cacheExistedServices GetRequest err: %v", err)
		return 0, err
	}
	revision = rsp.Header.Revision
	mlog.Infof("loadExistedServices GetResponse revision %v", revision)

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
				mlog.Infof("loadExistedServices addService, %s -> %s", key, value)
			}
		}
	}
	return revision, nil
}

// 加载和监听
func (e *etcdImp) loadAndWatch() error {
	e.mx.Lock()
	defer e.mx.Unlock()
	e.allServices = make(serviceContainer)
	revision, err := e.loadExistedServices()
	if err != nil {
		return err
	}
	rch := e.cli.Watch(e.ctx, e.prefix, clientv3.WithPrefix(), clientv3.WithRev(revision+1))
	if rch == nil {
		return err
	}
	e.rch = rch
	return nil
}

// 处理监听事件的错误
func (e *etcdImp) processWatchError(err error) error {
	switch err {
	case v3rpc.ErrCompacted:
		if err = e.loadAndWatch(); err != nil {
			return err
		}
	default:
		return err
	}
	return nil
}

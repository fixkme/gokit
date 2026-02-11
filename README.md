#### 后端kit，旨在快速构建游戏后端

##### 1、功能介绍
- wsg: 用[gnet/v2](https://github.com/panjf2000/gnet)实现的websocket gate server
   * 有限数量的协程处理io，支持跨协程路由数据，也支持在本io协程路由
   * 不支持压缩，因为RFC 6455只是把压缩作为可选功能，另外现在数据都是protobuf，有一定带宽的优化，并且可以在用户层进行压缩解压数据
- rpc: 用[netpoll](https://github.com/cloudwego/netpoll)实现的rpc server和client
   * 需要结合[protoc-gen-gom](https://github.com/fixkme/protoc-gen-gom)生成代码
   * 客户端支持异步调用和同步调用，超时处理
   * rpc框架没有解决“客户端认为超时，但是服务端已经完成”的问题，需要业务层按需处理，下单、扣款等敏感型业务是需要处理的，方法如下：
      - 业务层要遵循基本规则：客户端的timeout > 服务端的timeout
      - 每个请求加唯一id requestId，服务端处理后缓存结果，客户端超时的时候重试
      - 业务层将操作分成两段（请求受理、查询结果），服务端受理后缓存结果用于查询
      - 业务层在req.Meta设置deadline，服务端执行之前根据情况估计剩余时间是否操作，这个方法不保证一定能一致，但能减少不一致
- httpapi: 用gin实现http api 路由，通过rpc调用逻辑服务
- servicediscovery: 服务发现
- clock: 多层时间轮的定时器实现
- errs: Code+error封装的错误码
- ds: 数据结构
    * staticlist: 静态链表实现的FIFO队列，支持O(1)时间删除任意元素
    * skiplist: 跳表实现的排行榜
- util: 工具系列
    * time: 设置时区，修改时间，以及一系列跨天、跨周、跨月接口
- framework: 快速构建server app的框架，接近业务层，基本都是默认参数构建的模块
    * app: 栈形式运行module，一个module代表一个协程业务
    * config: 配置定义和加载
    * core: 框架核心模块，包括rpc、mongo、redis
    * go：协程worker封装

##### 2、实践例子
1. 本人开发的黑白棋[othello](https://github.com/fixkme/othello)

##### 3、待解决和优化
1. netpoll rpc server和client 写操作要不要用写协程替换mutex

#### 后端kit

##### 待解决和优化
1. netpoll rpc server和client 写操作要不要用写协程替换mutex
2. netpoll rpc client 怎么在真正写入fd的时候，将RST Broken pipe错误告知用户的callResult和AsyncCallResult
3. netpoll rpc client 增加CallAll方法 
4. ws gate server compression 实现
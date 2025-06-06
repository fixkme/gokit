package timer

type _Timer struct {
	id       int64             // ID
	when     int64             // 到期时间戳 毫秒
	data     any               // 数据
	receiver chan<- *Promise   // 处理器
	batch    chan<- []*Promise // 处理器
}

type Promise struct {
	TimerId int64
	NowTs   int64 // 当前时间戳 毫秒
	Data    any
}

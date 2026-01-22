package delta

type ICollector interface {
	Collect(key string, val any, ntf bool)
}

type IModel interface {
	SetCollector(syncID string, collector ICollector, cb func(string))
}

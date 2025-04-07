package discovery

type Discovery interface {
	Start() <-chan error

	Stop()

	RegisterService(serviceName string, rpcAddr string) (string, error)

	GetService(serviceName string) (rpcAddr string, err error)

	GetAllService(serviceName string) (rpcAddrs map[string]string, err error)
}

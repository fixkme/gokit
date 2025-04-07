package discovery

type Discovery interface {
	Start() <-chan error

	Stop()

	PutService(string, string) (string, error)

	GetService(serviceName string) (gprcAddr string, err error)

	GetAllService(serviceName string) (gprcAddr map[string]string, err error)
}

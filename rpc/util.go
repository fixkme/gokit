package rpc

import "net"

func getOneInnerIP() string {
	ips, err := getInnerIPs()
	if err != nil {
		return ""
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func getInnerIPs() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	var ips []string
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}

	return ips, nil
}

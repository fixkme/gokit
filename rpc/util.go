package rpc

import (
	"context"
	"net"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

// 获取本机内网IP
func GetOneInnerIP() string {
	ips, err := GetInnerIPs()
	if err != nil {
		return ""
	}
	if len(ips) > 0 {
		return ips[0]
	}
	return ""
}

// 获取本机所有内网IP
func GetInnerIPs() ([]string, error) {
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

// 同步调用
func SyncCall(ctx context.Context, cc *ClientConn, req proto.Message, outRsp proto.Message) (err error) {
	opt := &CallOption{
		Timeout: 3 * time.Second,
	}
	fullName := string(req.ProtoReflect().Descriptor().FullName())
	v2 := strings.SplitN(fullName, ".", 2)
	service, method := v2[0], v2[1][1:]
	_, _, err = cc.Invoke(ctx, service, method, req, outRsp, opt)
	return
}

// 异步调用，不需要回应
func AsyncCallWithoutResp(ctx context.Context, cc *ClientConn, req proto.Message) (err error) {
	opt := &CallOption{
		Async: true,
	}
	fullName := string(req.ProtoReflect().Descriptor().FullName())
	v2 := strings.SplitN(fullName, ".", 2)
	service, method := v2[0], v2[1][1:]
	_, _, err = cc.Invoke(ctx, service, method, req, nil, opt)
	return
}

// 异步调用，带有回应
func AsyncCallWithResp(ctx context.Context, cc *ClientConn, req proto.Message, outRsp proto.Message, outRet chan *AsyncCallResult, passData any) (err error) {
	opt := &CallOption{
		Async:        true,
		Timeout:      3 * time.Second,
		AsyncRetChan: outRet,
		PassThrough:  passData,
	}
	fullName := string(req.ProtoReflect().Descriptor().FullName())
	v2 := strings.SplitN(fullName, ".", 2)
	service, method := v2[0], v2[1][1:]
	_, _, err = cc.Invoke(ctx, service, method, req, outRsp, opt)
	return
}

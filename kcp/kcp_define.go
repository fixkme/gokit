package kcp

import "net"

const (
	mtuLimit = 1500
)

type SessionHandler interface {
	//GetConnId() string
	// 连接成功回调
	OnConnect(*Session)
	// data为提交给用户层的数据, 返回true表示可以继续OnRead
	OnRead(data []byte) bool
	// 连接断开
	OnClose()
}

type KcpOptions struct {
	KcpNoDelay        bool
	KcpNoCwnd         bool
	KcpUpdateInterval int // ms
	KcpFastResend     int
	KcpSendWnd        int
	KcpRecvWnd        int
	KcpMtu            int
	KcpMinRto         int // ms
	KcpStreamMode     bool
	ACKNoDelay        bool // kcp.Input 立即发送ack
}

type ServerOptions struct {
	KcpOptions
	// 对第一个udp消息进行验证，非法返回false, 成功需要返回SessionHandler, 会话id conv
	OnHandshake func(udpPacket []byte, raddr net.Addr) (sh SessionHandler, conv uint32, ok bool)
	// 解析连接唯一标识, 并且返回剩余bytes
	ParseConvId func(udpPacket []byte) (conv uint32, rest []byte)
}

package kcp

const (
	mtuLimit = 1500
)

type ClientInterface interface {
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
	// 对第一个udp消息进行验证，非法返回false, 成功需要返回client, 会话id conv
	OnHandshake func(udpPacket []byte, raddr string) (client ClientInterface, conv uint32, ok bool)
}

func (opt *ServerOptions) setDefault() {

}

package websocket

import "time"

const (
	// 业务处理协程池的默认容量
	DEFAULT_ANTS_POOL_SIZE = 1024 * 1024

	// 连接空闲超过该秒数将被自动关闭
	CONN_MAX_IDLE_SECONDS int64 = 600

	// 单条消息的接收上限, 超过后 coder/websocket 以 StatusMessageTooBig 关闭连接.
	// 对齐 tcp_fixed_head 的 MaxDataLength, 不再让对端一个长度声明就撑爆内存.
	MAX_MESSAGE_BYTES int64 = 16 << 20

	// 握手最长等待时间: 连上来不发完整 HTTP 请求的连接不能永久占住读协程
	HANDSHAKE_TIMEOUT time.Duration = 10 * time.Second

	// 单条下行消息的写超时, 对端长时间不读时用来断开连接
	SEND_TIMEOUT time.Duration = 10 * time.Second

	// 握手阶段解析 HTTP 请求/响应使用的缓冲区大小
	HANDSHAKE_BUFFER_SIZE = 4 << 10

	// 握手响应里带上的版本头, 沿用 gobwas 实现时的约定
	GO_VERSION_HEADER_KEY = "X-Go-Version-CCLehui"
)

package tcp_fixed_head

const (
	// 业务处理协程池的默认容量
	DefaultAntsPoolSize = 1024 * 1024

	// 协议头长度: Version(2) + ActionType(2) + DataLength(4)
	DefaultHeadLength = 8

	// 单帧 body 的长度上限。DataLength 是 uint32, 不设上限时一个伪造的超大长度
	// 会让连接一直等数据; 且 uint32 -> int 在 32 位平台会溢出成负数,
	// 变成 c.Next(负数), 而 gnet 的 Next(n<=0) 表示"读完整个缓冲区"
	MaxDataLength = 16 * 1024 * 1024

	PROTOCOL_VERSION = 0x8001 //协议版本 dec 32769

	// 默认监听地址: 所有网卡的 7001. 和 tcp_fixed_head 示例的默认端口一致.
	// 想只监听本机就传 WithProtoAddr("tcp://127.0.0.1:7001").
	DefaultProtoAddr = "tcp://:7001"

	//协议行为定义
	ACTION_PING = 0x0001 // ping行为
	ACTION_PONG = 0x0002 // pong行为
	ACTION_DATA = 0x00F0 // 业务行为
)

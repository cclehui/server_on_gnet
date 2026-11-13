package websocket

import (
	"context"

	coderws "github.com/coder/websocket"
	"github.com/panjf2000/ants/v2"
)

// ServerOption 在 NewServer 构造阶段修改 server 配置.
// 这些字段只在 gnet.Run 之前写, 启动后按只读使用.
type ServerOption func(server *WebSocketServer)

// WithHandler 设置业务处理函数. nil 表示不处理消息, dispatch 会打错误日志后丢弃.
func WithHandler(handler DataHandler) ServerOption {
	return func(server *WebSocketServer) {
		server.Handler = handler
	}
}

// WithWorkerPool 用调用方自己的 ants 池替换默认池.
// 传了就不再用 NewServer 里的默认池; nil 在构造期就拒绝, 否则第一条消息会在 Submit 时 panic.
func WithWorkerPool(workerPool *ants.Pool) ServerOption {
	if workerPool == nil {
		panic("websocket: WithWorkerPool got nil worker pool")
	}

	return func(server *WebSocketServer) {
		server.WorkerPool = workerPool
	}
}

// WithAcceptOptions 设置握手选项, 传了就不再使用 NewServer 的默认值(不校验 Origin).
// 要恢复 coder/websocket 的默认策略(校验 Origin)传 &coderws.AcceptOptions{}.
// nil 没有"用默认值"的含义, 直接拒绝, 免得调用方以为关掉了默认值.
func WithAcceptOptions(acceptOptions *coderws.AcceptOptions) ServerOption {
	if acceptOptions == nil {
		panic("websocket: WithAcceptOptions got nil options, use &coderws.AcceptOptions{} for library defaults")
	}

	return func(server *WebSocketServer) {
		server.AcceptOptions = acceptOptions
	}
}

// WithConnCloseHandler 设置连接关闭回调, 在 gnet 的 OnClose 里同步执行.
func WithConnCloseHandler(handler ConnCloseHandleFunc) ServerOption {
	return func(server *WebSocketServer) {
		server.ConnCloseHandler = handler
	}
}

// WithContext 设置 server 生命周期日志使用的 context. 不传时 NewServer 用 context.Background().
func WithContext(ctx context.Context) ServerOption {
	if ctx == nil {
		panic("websocket: WithContext got nil context")
	}

	return func(server *WebSocketServer) {
		server.ctx = ctx
	}
}

// WithConnIdleTimeout 设置空闲连接回收阈值(秒), 覆盖默认的 CONN_MAX_IDLE_SECONDS.
// 时间轮的格数和 tick 都在构造期按这个值算好, 所以只能在 NewServer 里设置, 启动后改无效.
func WithConnIdleTimeout(seconds int64) ServerOption {
	if seconds <= 0 {
		panic("websocket: WithConnIdleTimeout needs a positive number of seconds")
	}

	return func(server *WebSocketServer) {
		server.connIdleSeconds = seconds
	}
}

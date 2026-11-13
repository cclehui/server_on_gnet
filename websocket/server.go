package websocket

import (
	"context"
	stderrors "errors"
	"io"
	"net"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"github.com/pkg/errors"

	"github.com/cclehui/server_on_gnet/commonutil"
)

type WebSocketServer struct {
	Addr    string //ip:port
	ConnNum int64

	Handler    DataHandler //业务处理具体实现
	WorkerPool *ants.Pool  //业务处理协程池

	// 握手选项. 默认 InsecureSkipVerify(等价于旧的"接受任何 Host"),
	// 要按 Origin 拦截就用 WithAcceptOptions 显式给 OriginPatterns.
	AcceptOptions *coderws.AcceptOptions

	connTimeWheel *TimeWheel //连接管理时间轮

	connIdleSeconds int64 //连接空闲回收阈值(秒), 同时决定时间轮槽数

	ConnCloseHandler ConnCloseHandleFunc //连接关闭处理

	ctx context.Context
}

// NewEchoServer 是带默认 echo handler 的 NewServer.
// echo 作为第一个 option 传入, 所以调用方的 WithHandler 仍然能覆盖它.
func NewEchoServer(addr string, opts ...ServerOption) *WebSocketServer {
	echoOpts := make([]ServerOption, 0, len(opts)+1)
	echoOpts = append(echoOpts, WithHandler(EchoDataHandler))
	echoOpts = append(echoOpts, opts...)

	return NewServer(addr, echoOpts...)
}

// NewServer 构造 server. 先应用 opts, 再给没被 option 设置过的字段填默认值:
// option 里给了什么就用什么, 不会先建一个默认值再被覆盖掉(order: opts → defaults).
// 后传的 option 覆盖先传的.
func NewServer(addr string, opts ...ServerOption) *WebSocketServer {
	server := &WebSocketServer{}

	//ip 和端口
	server.Addr = addr

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(server)
	}

	// 下面每个默认值只在对应字段仍为空时生效.
	// 池和时间轮都会真正分配资源, 先建默认值再被覆盖就是纯泄漏(池容量 1024*1024).
	if server.AcceptOptions == nil {
		server.AcceptOptions = newDefaultAcceptOptions()
	}

	if server.ctx == nil {
		server.ctx = context.Background()
	}

	if server.connIdleSeconds == 0 {
		server.connIdleSeconds = CONN_MAX_IDLE_SECONDS
	}

	if server.WorkerPool == nil {
		server.WorkerPool = newDefaultWorkerPool() //业务处理协程池
	}

	// 时间轮的 tick 和槽数按最终的 connIdleSeconds 建, 必须在默认值都定下来之后
	if server.connTimeWheel == nil {
		server.connTimeWheel = newTimeWheel(time.Second, int(server.connIdleSeconds), timeWheelJob)
	}

	return server
}

func newDefaultWorkerPool() *ants.Pool {
	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	// 构造函数忽略池错误, 与 tcp_fixed_head 的写法保持一致
	defaultAntsPool, _ := ants.NewPool(DEFAULT_ANTS_POOL_SIZE, ants.WithOptions(options))

	return defaultAntsPool
}

// newDefaultAcceptOptions 保留 gobwas 版本"接受任何 Host"的行为:
// coder/websocket 默认校验 Origin, 不关掉会直接拒绝跨域握手.
// 对外暴露时请改成 OriginPatterns: []string{"your.example.com"}.
func newDefaultAcceptOptions() *coderws.AcceptOptions {
	return &coderws.AcceptOptions{
		InsecureSkipVerify: true,
	}
}

// 当前连接数, 理由同 tcp_fixed_head.TCPFixHeadServer.GetConnNum
func (server *WebSocketServer) GetConnNum() int64 {
	return atomic.LoadInt64(&server.ConnNum)
}

func (server *WebSocketServer) OnBoot(eng gnet.Engine) (action gnet.Action) {
	commonutil.GetLogger().Infof(server.ctx, "[server] websocket server 监听在 %s", server.Addr)

	//运行连接管理
	server.connTimeWheel.Start()
	commonutil.GetLogger().Infof(server.ctx, "[server] websocket 连接管理时间轮已启动")

	return
}

func (server *WebSocketServer) OnShutdown(eng gnet.Engine) {
	commonutil.GetLogger().Infof(server.ctx, "[server] websocket server shutdown on %s", server.Addr)
}

func (server *WebSocketServer) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	totalNum := atomic.AddInt64(&server.ConnNum, 1)

	commonutil.GetLogger().Infof(server.ctx,
		"[server] 连接数=%d, 新连接: %s", totalNum, c.RemoteAddr())

	wsConn := NewWSConn(c)
	c.SetContext(wsConn)

	// 握手和读帧都是阻塞 io, 只能在独立协程里做; reactor 只管搬运字节
	go server.serveConn(wsConn)

	return
}

func (server *WebSocketServer) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	atomic.AddInt64(&server.ConnNum, -1)

	wsConn, ok := c.Context().(*WSConn)
	if !ok || wsConn == nil {
		commonutil.GetLogger().Debugf(server.ctx, "[server] 关闭连接: %s", c.RemoteAddr())
		return
	}

	// 关闭队列会唤醒还阻塞在 Read 上的读协程, 让它自己退出并把 coder/websocket 连接收尾
	wsConn.inbound.Close()

	if server.ConnCloseHandler != nil {
		server.ConnCloseHandler(wsConn)
	}

	commonutil.GetLogger().Debugf(server.ctx, "[server] 关闭连接: %s", c.RemoteAddr())

	return
}

func (server *WebSocketServer) OnTick() (delay time.Duration, action gnet.Action) {
	return
}

func (server *WebSocketServer) React(frame []byte, c gnet.Conn) (out []byte, action gnet.Action) {
	return
}

// reactor 只做一件事: 把收到的字节拷贝给本连接的读协程.
// 不在这里解帧, 也不在这里跑业务逻辑.
func (server *WebSocketServer) OnTraffic(c gnet.Conn) (action gnet.Action) {
	wsConn, ok := c.Context().(*WSConn)
	if !ok {
		// OnOpen 里已经 SetContext, 走到这里说明 Context 被别处改坏了
		commonutil.GetLogger().Errorf(server.ctx, "[server] OnTraffic context 数据格式异常")

		return gnet.Close
	}

	// Read 会拷贝出当前可用字节; 用 Read 而不是 Next 是因为 Next 返回的是借用缓冲区,
	// 不能交给读协程
	n := c.InboundBuffered()
	if n <= 0 {
		return gnet.None
	}

	buf := make([]byte, n)
	readN, err := c.Read(buf)
	if readN > 0 {
		wsConn.appendInbound(buf[:readN])
	}

	if err != nil && readN == 0 {
		commonutil.GetLogger().Errorf(server.ctx,
			"[server] 读取连接数据失败, from:%s, err:%+v", wsConn.remoteAddr, err)

		return gnet.Close
	}

	return gnet.None
}

// 每连接一个读协程: 先握手, 再循环读消息并分发
func (server *WebSocketServer) serveConn(wsConn *WSConn) {
	ctx := context.Background()

	// 握手阶段也要有个上限, 否则半截 HTTP 请求能把读协程和连接一直挂住
	_ = wsConn.SetReadDeadline(time.Now().Add(HANDSHAKE_TIMEOUT))

	wsConnProto, err := server.accept(wsConn)
	if err != nil {
		commonutil.GetLogger().Errorf(ctx, "[server] ws 协议升级异常, from:%s, err:%+v",
			wsConn.remoteAddr, err)

		// Accept 失败时可能已经写过 HTTP 错误响应, 这里只需关闭底层连接
		_ = wsConn.GnetConn.Close()

		return
	}

	_ = wsConn.SetReadDeadline(time.Time{})

	wsConn.setCoderConn(wsConnProto)
	wsConnProto.SetReadLimit(MAX_MESSAGE_BYTES)

	commonutil.GetLogger().Infof(ctx, "[server] ws 协议升级成功, from:%s", wsConn.remoteAddr)

	//升级成功, 开始计时空闲
	server.updateConnActiveTs(wsConn)

	defer func() {
		// 读循环退出(对端关闭/读超时/连接被关)后关闭协议连接.
		// Close 会再读一次等对端的 close 握手, 底层连接被关时它会立刻返回.
		_ = wsConnProto.Close(coderws.StatusNormalClosure, "")
	}()

	for {
		msgType, message, err := wsConnProto.Read(ctx)
		if err != nil {
			server.logReadExit(wsConn, err)

			return
		}

		server.dispatch(wsConn, msgType, message)
	}
}

func (server *WebSocketServer) dispatch(wsConn *WSConn, msgType coderws.MessageType, message []byte) {
	ctx := context.Background()

	commonutil.GetLogger().Infof(ctx, "[server] 收到消息, from:%s, type:%d, payload:%q",
		wsConn.remoteAddr, msgType, string(message))

	//更新连接活跃时间
	server.updateConnActiveTs(wsConn)

	if server.Handler == nil {
		commonutil.GetLogger().Errorf(ctx, "[server] 未设置 DataHandler, 丢弃消息, from:%s", wsConn.remoteAddr)

		return
	}

	// message 是 coder/websocket 每次 Read 新分配的, 可以安全地交给 worker 协程
	err := server.WorkerPool.Submit(func() {
		//具体业务在 worker pool中处理
		server.Handler(&DataHandlerParam{
			Request:     message,
			MessageType: msgType,
			WSConn:      wsConn,
			Server:      server,
		})
	})

	if err != nil {
		commonutil.GetLogger().Errorf(ctx, "[server] 消息提交处理协程池失败, from:%s, err:%+v",
			wsConn.remoteAddr, err)
	}
}

// 正常关闭(对端 close 帧 / 本端 eviction)不当作错误刷日志
func (server *WebSocketServer) logReadExit(wsConn *WSConn, err error) {
	status := coderws.CloseStatus(err)

	switch {
	case status == coderws.StatusNormalClosure, status == coderws.StatusGoingAway:
		commonutil.GetLogger().Infof(server.ctx, "[server] 客户端关闭连接, from:%s, status:%d",
			wsConn.remoteAddr, status)

	case stderrors.Is(err, net.ErrClosed) || stderrors.Is(err, io.EOF):
		commonutil.GetLogger().Debugf(server.ctx, "[server] 连接已关闭, from:%s", wsConn.remoteAddr)

	default:
		commonutil.GetLogger().Errorf(server.ctx, "[server] 读取客户端消息失败, from:%s, err:%+v",
			wsConn.remoteAddr, err)
	}
}

// 发送下行消息
func (server *WebSocketServer) SendDownStreamMsg(wsConn *WSConn, msgType coderws.MessageType, msg []byte) error {
	if wsConn == nil {
		return errors.New("SendDownStreamMsg wsConn is nil")
	}

	server.updateConnActiveTs(wsConn)

	return wsConn.WriteMessage(msgType, msg)
}

// 更新连接活跃时间
func (server *WebSocketServer) updateConnActiveTs(wsConn *WSConn) {
	if wsConn == nil {
		return
	}

	now := time.Now().Unix()

	if now-atomic.LoadInt64(&wsConn.LastActiveTs) > server.connIdleSeconds {
		jobParam := &jobParam{server, wsConn}
		server.connTimeWheel.AddTimer(time.Second*time.Duration(server.connIdleSeconds), nil, jobParam)
	}

	wsConn.UpdateActiveTsToNow()
}

// 关闭连接.
// 已握手的连接走协议层的 close 握手(对端会收到 StatusGoingAway), 未握手直接断开.
// 调用方可能是时间轮/业务协程, 所以这里不能阻塞: Close 内部有 5s 的收发超时,
// 超时后会关掉底层连接, 读协程随之退出.
func (server *WebSocketServer) closeConn(wsConn *WSConn) {
	if wsConn == nil {
		return
	}

	wsConnProto := wsConn.coderConn()
	if wsConnProto == nil {
		_ = wsConn.GnetConn.Close()

		return
	}

	go func() {
		err := wsConnProto.Close(coderws.StatusGoingAway, "idle timeout")
		if err != nil && !stderrors.Is(err, net.ErrClosed) {
			commonutil.GetLogger().Debugf(server.ctx, "[server] 关闭 ws 连接异常, from:%s, err:%+v",
				wsConn.remoteAddr, err)
		}
	}()
}

// 连接关闭处理函数
type ConnCloseHandleFunc func(wsConn *WSConn)

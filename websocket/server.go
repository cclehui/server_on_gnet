package websocket

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
)

type WebSocketServer struct {
	Addr    string //ip:port
	ConnNum int64

	Handler    DataHandler //业务处理具体实现
	WorkerPool *ants.Pool  //业务处理协程池

	connTimeWheel *TimeWheel //连接管理时间轮

	ConnCloseHandler ConnCloseHandleFunc //连接关闭处理

	ctx context.Context
}

func NewEchoServer(addr string) *WebSocketServer {

	server := NewServer(addr)

	server.Handler = EchoDataHandler

	return server
}

func NewServer(addr string) *WebSocketServer {
	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	server := &WebSocketServer{}

	//ip 和端口
	server.Addr = addr
	server.WorkerPool = defaultAntsPool //业务处理协程池

	//连接管理时间轮
	server.connTimeWheel = newTimeWheel(time.Second, int(ConnMaxIdleSeconds), timeWheelJob)
	//server.connTimeWheel = newTimeWheel(time.Second, ConnMaxIdleSeconds*2, timeWheelJob)

	server.ctx = context.Background()

	return server
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

	return
}

func (server *WebSocketServer) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	atomic.AddInt64(&server.ConnNum, -1)

	commonutil.GetLogger().Debugf(server.ctx, "[server] 关闭连接: %s", c.RemoteAddr())

	return
}

func (server *WebSocketServer) OnTick() (delay time.Duration, action gnet.Action) {
	return
}

func (server *WebSocketServer) React(frame []byte, c gnet.Conn) (out []byte, action gnet.Action) {
	return
}

func (server *WebSocketServer) OnTraffic(c gnet.Conn) (action gnet.Action) {
	//fmt.Printf("react, 当前全部数据, 1111, %v\n", c.Read())
	//fmt.Printf("react, 当前全部数据, 2222, %s\n", string(c.Read()))
	ctx := context.Background()

	if c.Context() == nil {
		//初始化协议升级器
		upgraderConn := NewDefaultUpgrader(c)

		c.SetContext(upgraderConn)
	}

	upgraderConn, ok := c.Context().(*GnetUpgraderConn)
	if !ok {
		err := errors.New("react context 数据格式异常")

		commonutil.GetLogger().Errorf(ctx, "[server] react context 数据格式异常: %+v", err)

		return gnet.None
	}

	if !upgraderConn.IsSuccessUpgraded {
		//协议升级过程
		_, err := upgraderConn.Upgrader.Upgrade(upgraderConn)

		if err != nil {
			commonutil.GetLogger().Errorf(ctx, "[server] ws 协议升级异常: %+v", err)
			return gnet.Close

		} else {
			commonutil.GetLogger().Infof(ctx, "[server] ws 协议升级成功, from:%s", upgraderConn.GnetConn.RemoteAddr().String())
			upgraderConn.IsSuccessUpgraded = true
			//upgraderConn.UniqId = int(server.ConnNum) //cclehui

			//更新连接活跃时间
			server.updateConnActiveTs(upgraderConn)

			//cclehui_todo  维护连接id id pool
		}

	} else {
		//正常的数据处理过程

		//在 reactor 协程中做解码操作
		//msg, op, err := wsutil.ReadClientData(upgraderConn)
		//frame, err := ws.ReadFrame(upgraderConn)
		messages, err := wsutil.ReadClientMessage(upgraderConn, nil)

		if err == nil {
			//log.Printf("本次收到的消息, op:%v,  msg:%v\n", op, msg)

			for _, message := range messages {

				switch message.OpCode {
				case ws.OpPing:
					commonutil.GetLogger().Infof(ctx, "[server] 收到 PING, from:%s, message:%v",
						upgraderConn.GnetConn.RemoteAddr(), message)
					wsutil.WriteServerMessage(upgraderConn, ws.OpPong, nil)

					//更新连接活跃时间
					server.updateConnActiveTs(upgraderConn)

				case ws.OpText:
					commonutil.GetLogger().Infof(ctx, "[server] 收到 TEXT, from:%s, payload:%q",
						upgraderConn.GnetConn.RemoteAddr(), string(message.Payload))

					server.WorkerPool.Submit(func() {
						//具体业务在 worker pool中处理
						handlerParam := &DataHandlerParam{}

						handlerParam.OpCode = message.OpCode
						handlerParam.Request = message.Payload
						handlerParam.Writer = upgraderConn
						handlerParam.WSConn = upgraderConn
						handlerParam.Server = server

						server.Handler(handlerParam)
					})

					//更新连接活跃时间
					server.updateConnActiveTs(upgraderConn)

				case ws.OpClose:
					commonutil.GetLogger().Infof(ctx, "[server] 收到客户端 CLOSE, from:%s, payload:%q",
						upgraderConn.GnetConn.RemoteAddr(), string(message.Payload))
					//关闭连接
					server.closeConn(upgraderConn)

					return gnet.Close

				default:
					commonutil.GetLogger().Infof(ctx, "[server] 收到暂不支持的 opcode:%v, from:%s, message:%v",
						message.OpCode, upgraderConn.GnetConn.RemoteAddr(), message)
				}
			}

		} else {
			commonutil.GetLogger().Errorf(ctx, "[server] 读取客户端消息失败, from:%s, message:%v, err:%v",
				upgraderConn.GnetConn.RemoteAddr(), messages, err)
		}
	}

	return gnet.None
}

// 发送下行消息
func (server *WebSocketServer) SendDownStreamMsg(wsConn *GnetUpgraderConn, opcode ws.OpCode, msg []byte) error {
	if wsConn == nil {
		return errors.New("SendDownStreamMsg wsConn is nil")
	}

	server.updateConnActiveTs(wsConn)

	return wsutil.WriteServerMessage(*wsConn, opcode, msg)
}

// 更新连接活跃时间
func (server *WebSocketServer) updateConnActiveTs(wsConn *GnetUpgraderConn) {

	now := time.Now().Unix()

	if now-wsConn.LastActiveTs > ConnMaxIdleSeconds {
		jobParam := &jobParam{server, wsConn}
		server.connTimeWheel.AddTimer(time.Second*time.Duration(ConnMaxIdleSeconds), nil, jobParam)
	}

	wsConn.LastActiveTs = now
}

// 关闭连接
func (server *WebSocketServer) closeConn(wsConn *GnetUpgraderConn) {
	if wsConn == nil {
		return
	}

	ws.WriteFrame(wsConn, ws.NewCloseFrame(nil))

	if server.ConnCloseHandler != nil {
		server.ConnCloseHandler(wsConn)
	}
}

// 连接关闭处理函数
type ConnCloseHandleFunc func(wsConn *GnetUpgraderConn)

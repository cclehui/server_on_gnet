package websocket

import (
	"log"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/panjf2000/ants"
	"github.com/panjf2000/gnet"
)

type WebSocketServer struct {
	*gnet.EventServer
	IP      string
	Port    int
	ConnNum int64

	Handler    DataHandler //业务处理具体实现
	WorkerPool *ants.Pool  //业务处理协程池

	connTimeWheel *TimeWheel //连接管理时间轮

	ConnCloseHandler ConnCloseHandleFunc //连接关闭处理
}

func NewEchoServer(port int) *WebSocketServer {

	server := NewServer(port)

	server.Handler = EchoDataHandler

	return server
}

func NewServer(port int) *WebSocketServer {

	options := ants.Options{ExpiryDuration: time.Second * 10, Nonblocking: true}
	defaultAntsPool, _ := ants.NewPool(DefaultAntsPoolSize, ants.WithOptions(options))

	server := &WebSocketServer{}

	//ip 和端口
	server.IP = "localhost" //cclehui_test
	server.Port = port
	server.WorkerPool = defaultAntsPool //业务处理协程池

	//连接管理时间轮
	server.connTimeWheel = newTimeWheel(time.Second, int(ConnMaxIdleSeconds), timeWheelJob)
	//server.connTimeWheel = newTimeWheel(time.Second, ConnMaxIdleSeconds*2, timeWheelJob)

	return server
}

func (server *WebSocketServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("websocket server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumLoops)

	//运行连接管理
	server.connTimeWheel.Start()
	log.Printf("websocket server connTimeWheel start\n")

	return
}

func (server *WebSocketServer) OnOpened(c gnet.Conn) (out []byte, action gnet.Action) {
	atomic.AddInt64(&server.ConnNum, 1)
	return
}

func (server *WebSocketServer) OnClosed(c gnet.Conn, err error) (action gnet.Action) {
	atomic.AddInt64(&server.ConnNum, -1)
	return
}

func (server *WebSocketServer) React(c gnet.Conn) (out []byte, action gnet.Action) {

	//fmt.Printf("react, 当前全部数据, 1111, %v\n", c.Read())
	//fmt.Printf("react, 当前全部数据, 2222, %s\n", string(c.Read()))

	if c.Context() == nil {
		//初始化协议升级器
		upgraderConn := NewDefaultUpgrader(c)

		c.SetContext(upgraderConn)
	}

	if upgraderConn, ok := c.Context().(*GnetUpgraderConn); ok {

		if !upgraderConn.IsSuccessUpgraded {
			//协议升级过程
			_, err := upgraderConn.Upgrader.Upgrade(upgraderConn)

			if err != nil {
				log.Printf("react ws 协议升级异常， %v\n", err)

			} else {
				log.Printf("react ws 协议升级成功, %s\n", upgraderConn.GnetConn.RemoteAddr().String())
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
						log.Printf("ping, message:%v\n", message)
						wsutil.WriteServerMessage(upgraderConn, ws.OpPong, nil)

						//更新连接活跃时间
						server.updateConnActiveTs(upgraderConn)

					case ws.OpText:
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
						log.Printf("client关闭连接, Payload:%s,  error:%v\n", string(message.Payload), nil)
						//关闭连接
						server.closeConn(upgraderConn)

						return nil, gnet.Close

					default:
						log.Printf("操作暂不支持, message:%v,  error:%v\n", message)
					}
				}

			} else {
				log.Printf("本次收到的消息不完整, message:%v,  error:%v\n", messages, err)
			}

		}

	} else {
		log.Println("react contenxt 数据格式异常")
	}

	return
}

//更新连接活跃时间
func (server *WebSocketServer) updateConnActiveTs(wsConn *GnetUpgraderConn) {

	now := time.Now().Unix()

	if now-wsConn.LastActiveTs > ConnMaxIdleSeconds {
		jobParam := &jobParam{server, wsConn}
		server.connTimeWheel.AddTimer(time.Second*time.Duration(ConnMaxIdleSeconds), nil, jobParam)
	}

	wsConn.LastActiveTs = now
}

//关闭连接
func (server *WebSocketServer) closeConn(wsConn *GnetUpgraderConn) {
	if wsConn == nil {
		return
	}

	ws.WriteFrame(wsConn, ws.NewCloseFrame(nil))

	if server.ConnCloseHandler != nil {
		server.ConnCloseHandler(wsConn)
	}
}

//连接关闭处理函数
type ConnCloseHandleFunc func(wsConn *GnetUpgraderConn)

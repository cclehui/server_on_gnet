package websocket

import (
	"log"
	"net/http"
	"runtime"
	"time"

	"github.com/gobwas/ws"
	"github.com/panjf2000/gnet"
)

//协议升级, conn处理 业务层conn
type GnetUpgraderConn struct {
	GnetConn gnet.Conn

	UniqId string //连接的全局唯一id ?

	LastActiveTs int64 //上次活跃的时间 unix 时间戳

	IsSuccessUpgraded bool
	Upgrader          ws.Upgrader
}

//连接超时管理函数
func timeWheelJob(param *jobParam) {
	if param == nil || param.wsConn == nil || param.server == nil {
		return
	}

	diffNow := time.Now().Unix() - param.wsConn.LastActiveTs

	if diffNow > ConnMaxIdleSeconds {
		//长时间未活跃
		log.Printf("server关闭连接, 连接空闲%d秒, %v\n", ConnMaxIdleSeconds, param.wsConn)
		//关闭连接
		param.server.closeConn(param.wsConn)

	} else {
		//进入新的时间循环
		param.server.connTimeWheel.AddTimer(time.Second*time.Duration((ConnMaxIdleSeconds-diffNow)), nil, param)
	}

}

// 读数据 这里为什么没用 *GnetUpgraderConn ?
func (u GnetUpgraderConn) Read(b []byte) (n int, err error) {

	targetLength := len(b)
	if targetLength < 1 {
		return 0, nil
	}

	if u.GnetConn.BufferLength() >= targetLength {
		//buffer中数据够
		curNum, realData := u.GnetConn.ReadN(targetLength)

		n = curNum

		copy(b, realData) //数据拷贝

	} else {
		//buffer 中数据不够
		allData := u.GnetConn.Read()
		u.GnetConn.ResetBuffer() //数据已全部读出来

		copy(b, allData) //数据拷贝

		n = len(allData)
	}

	return n, nil
}

//写数据 这里为什么没用 *GnetUpgraderConn ?
func (u GnetUpgraderConn) Write(b []byte) (n int, err error) {

	u.GnetConn.AsyncWrite(b)

	return len(b), nil
}

//更新连接活跃时间到当前时间
func (u *GnetUpgraderConn) UpdateActiveTsToNow() {
	u.LastActiveTs = time.Now().Unix()

}

//默认的协议升级类
func NewDefaultUpgrader(conn gnet.Conn) *GnetUpgraderConn {
	return &GnetUpgraderConn{
		GnetConn: conn,

		Upgrader:          defaultUpgrader,
		IsSuccessUpgraded: false,
	}
}

func NewEmptyUpgrader(conn gnet.Conn) *GnetUpgraderConn {
	return &GnetUpgraderConn{
		GnetConn: conn,

		Upgrader:          emptyUpgrader,
		IsSuccessUpgraded: false,
	}
}

// Prepare handshake header writer from http.Header mapping.
var header = ws.HandshakeHeaderHTTP(http.Header{
	"X-Go-Version-CCLehui": []string{runtime.Version()},
})

//空的协议升级类
var emptyUpgrader = ws.Upgrader{}

//默认的协议升级处理类
var defaultUpgrader = ws.Upgrader{
	OnHost: func(host []byte) error {
		if string(host) == "github.com" {
			return nil
		}

		log.Printf("ws OnHost:%s\n", string(host))

		return nil
	},

	OnHeader: func(key, value []byte) error {
		log.Printf("ws OnHeader, key:%s, value:%s\n", string(key), string(value))

		return nil
	},
	OnBeforeUpgrade: func() (ws.HandshakeHeader, error) {
		log.Printf("ws OnBeforeUpgrade\n")
		return header, nil
	},

	OnRequest: func(uri []byte) error {
		log.Printf("ws OnRequest: data uri: %v\n", string(uri))
		return nil
	},
}

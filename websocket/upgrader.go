package websocket

import (
	"log"
	"net/http"
	"runtime"

	"github.com/gobwas/ws"
	"github.com/panjf2000/gnet"
)

//协议升级, conn处理
type GnetUpgraderConn struct {
	GnetConn gnet.Conn

	IsSuccessUpgraded bool
	Upgrader          ws.Upgrader
}

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

func (u GnetUpgraderConn) Write(b []byte) (n int, err error) {

	u.GnetConn.AsyncWrite(b)

	return len(b), nil
}

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

var emptyUpgrader = ws.Upgrader{}

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

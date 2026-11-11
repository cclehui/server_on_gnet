package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/cclehui/server_on_gnet/tcp_fixed_head"
	"github.com/panjf2000/gnet/v2"
)

// 固定头部长度协议示例
//
// 帧格式 (大端):
//
//	Version uint16(0x8001) | ActionType uint16 | DataLength uint32 | Data[DataLength]
//
// 示例做的事:
//  1. 起一个 gnet server, 业务处理用本文件的 demoHandler: 收到 PING 回 PONG
//  2. 并发起 4 个客户端, 每个客户端依次发 3 个 PING, 并读回对应的 PONG
//  3. 其中一个客户端在第 2 步之前插入 3 个非法字节, 用来演示服务端如何拒绝
//     非法协议头 —— 预期结果是服务端关掉这条连接, 所以那个客户端收不到 3 个 PONG
//
// 所以日志里出现 PROTOCOL_VERSION error / connection reset 是设计好的演示结果,
// 不是程序 bug。
//
// go run examples/tcp_fixed_head/tcp_fixed_head_server.go
func main() {
	var port int
	var multicore bool

	flag.IntVar(&port, "port", 7001, "server port")
	flag.BoolVar(&multicore, "multicore", true, "multicore")
	flag.Parse()

	ctx := context.Background()

	printBanner(port)

	tcpServer := tcp_fixed_head.NewTCPFixHeadServer(port)
	// 不设置 Handler 时用的是库里的 defaultHandler(同样是 PING->PONG),
	// 这里显式替换是为了让日志能打上 action 和 peer 地址, 读起来更直观。
	tcpServer.Handler = demoHandler

	clients := demoClients(port)

	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)

		c := c
		go func() {
			defer wg.Done()
			c.run(ctx)
		}()
	}

	go func() {
		wg.Wait()
		commonutil.GetLogger().Infof(ctx,
			"[server] 演示结束: %d 个客户端均已退出 (client-2 是被协议校验主动断开的)", len(clients))

		// client 侧 Close() 返回不代表 server 侧 OnClose 已经跑完, 这里等连接真正回收
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			if tcpServer.GetConnNum() == 0 {
				break
			}

			time.Sleep(50 * time.Millisecond)
		}

		commonutil.GetLogger().Infof(ctx, "[server] 连接已全部回收, 当前连接数=%d", tcpServer.GetConnNum())
	}()

	options := []gnet.Option{
		gnet.WithReusePort(true),
		gnet.WithMulticore(multicore),
		gnet.WithTCPKeepAlive(time.Second * 90),
	}

	err := tcp_fixed_head.Run(tcpServer, fmt.Sprintf("tcp://:%d", port), options...)
	if err != nil {
		commonutil.GetLogger().Errorf(ctx, "启动失败:%+v", err)
	}
}

func printBanner(port int) {
	fmt.Println("================================================================")
	fmt.Println("tcp_fixed_head 示例")
	fmt.Printf("帧格式: Version(2B, 0x%04X) + ActionType(2B) + DataLength(4B) + Data\n",
		tcp_fixed_head.PROTOCOL_VERSION)
	fmt.Printf("服务端: tcp://localhost:%d, 业务处理: PING -> PONG\n", port)
	fmt.Println("客户端:")
	fmt.Println("  client-1 / 3 / 4 : 正常发送 3 个 PING, 预期各收到 3 个 PONG")
	fmt.Println("  client-2         : 第 2 步前插入 3 字节非法数据, 预期被服务端断开")
	fmt.Println("下面是结构化日志: [server] 是服务端, [client-N] 是第 N 个客户端")
	fmt.Println("用 grep 'server]' 只看服务端, grep 'client-' 只看客户端")
	fmt.Println("================================================================")
}

// 4 个客户端和各条连接是并发建立的, 只看 [::1]:57709 这种地址对不上是哪个客户端。
// 这里记一份 本地地址 -> client-N 的映射, 让服务端日志也能直接点名。
var (
	peerNamesMu sync.RWMutex
	peerNames   = map[string]string{}
)

func registerPeer(localAddr net.Addr, name string) {
	if localAddr == nil {
		return
	}

	peerNamesMu.Lock()
	peerNames[localAddr.String()] = name
	peerNamesMu.Unlock()
}

func peerName(c gnet.Conn) string {
	if c == nil || c.RemoteAddr() == nil {
		return "unknown"
	}

	addr := c.RemoteAddr().String()

	peerNamesMu.RLock()
	name, ok := peerNames[addr]
	peerNamesMu.RUnlock()

	if !ok {
		// server 侧看到的地址和 client 侧 LocalAddr() 不一定字面相同(比如 ::1 后缀), 做一次兜底
		peerNamesMu.RLock()
		for k, v := range peerNames {
			if strings.HasSuffix(k, addr) || strings.HasSuffix(addr, k) {
				name, ok = v, true
				break
			}
		}
		peerNamesMu.RUnlock()
	}

	if !ok {
		return addr
	}

	return fmt.Sprintf("%s(%s)", name, addr)
}

// demoHandler 演示如何替换默认业务处理: 收到 PING 回一个 PONG。
func demoHandler(hctx *tcp_fixed_head.HandlerContext) {
	if hctx.ProtocolData == nil {
		return
	}

	protocol := tcp_fixed_head.NewTCPFixHeadProtocol()
	peer := peerName(hctx.Conn)

	commonutil.GetLogger().Infof(hctx, "[server] 收到 %s, from:%s, data:%q",
		tcp_fixed_head.ActionName(hctx.ProtocolData.ActionType), peer, string(hctx.ProtocolData.Data))

	if hctx.ProtocolData.ActionType != tcp_fixed_head.ACTION_PING || hctx.Conn == nil {
		return
	}

	pongData, err := protocol.EncodeData(tcp_fixed_head.ACTION_PONG,
		[]byte(fmt.Sprintf("pong, %s", string(hctx.ProtocolData.Data))))
	if err != nil {
		commonutil.GetLogger().Errorf(hctx, "[server] 编码 PONG 失败, err:%v", err)
		return
	}

	if err := hctx.Conn.AsyncWrite(pongData, func(c gnet.Conn, err error) error { return nil }); err != nil {
		commonutil.GetLogger().Errorf(hctx, "[server] 回写 PONG 失败, err:%v", err)
		return
	}

	commonutil.GetLogger().Infof(hctx, "[server] 回复 PONG, to:%s", peer)
}

const packetsPerClient = 3

type demoClient struct {
	name string
	port int
	// injectJunk 为 true 时, 在第 2 个 PING 之前插入非法字节
	injectJunk bool
}

func demoClients(port int) []demoClient {
	return []demoClient{
		{name: "client-1", port: port},
		{name: "client-2", port: port, injectJunk: true},
		{name: "client-3", port: port},
		{name: "client-4", port: port},
	}
}

func (c demoClient) run(ctx context.Context) {
	logf := func(format string, args ...interface{}) {
		all := make([]interface{}, 0, len(args)+1)
		all = append(all, c.name)
		all = append(all, args...)
		commonutil.GetLogger().Infof(ctx, "[%s] "+format, all...)
	}

	conn, err := dialWithRetry(c.port)
	if err != nil {
		logf("连接服务端失败: %v", err)
		return
	}
	defer conn.Close()

	registerPeer(conn.LocalAddr(), c.name)
	logf("client 已连接, 本地端口 %s", conn.LocalAddr())

	protocol := tcp_fixed_head.NewTCPFixHeadProtocol()

	for i := 1; i <= packetsPerClient; i++ {
		if c.injectJunk && i == 2 {
			junk := []byte("xxx")

			if _, err := conn.Write(junk); err != nil {
				c.reportDisconnect(logf, i, err)
				return
			}

			logf("第 %d/%d 步: 故意写入 %d 字节非法数据 %q (不是合法帧头, 预期服务端因此断开)",
				i, packetsPerClient, len(junk), junk)
		}

		// 数据: 第 i 步发 i 个重复数字 + "abc", 即 1abc / 22abc / 333abc
		data := strings.Repeat(strconv.Itoa(i), i) + "abc"

		frame, err := protocol.EncodeData(tcp_fixed_head.ACTION_PING, []byte(data))
		if err != nil {
			logf("第 %d/%d 步: 编码 PING 失败: %v", i, packetsPerClient, err)
			return
		}

		logf("第 %d/%d 步: client 发送 PING, data=%q, 整帧 %d 字节", i, packetsPerClient, data, len(frame))

		if _, err := conn.Write(frame); err != nil {
			c.reportDisconnect(logf, i, err)
			return
		}

		// AsyncWrite 是异步的, 用一个读超时避免永远卡住
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

		resp, err := protocol.ClientDecode(conn)
		if err != nil {
			c.reportDisconnect(logf, i, err)
			return
		}

		logf("第 %d/%d 步: client 收到 %s", i, packetsPerClient, formatFrame(resp))

		time.Sleep(time.Second)
	}

	logf("完成: %d 个 PING 全部收到 PONG", packetsPerClient)
}

// reportDisconnect 区分"预期内的断开"和"真的出错了", 避免把演示结果打成 ERROR
func (c demoClient) reportDisconnect(logf func(string, ...interface{}), step int, err error) {
	if c.injectJunk {
		logf("第 %d/%d 步: 连接被服务端断开 (%v)", step, packetsPerClient, err)
		logf("这是预期结果: 上一步写入的非法字节让服务端解析出错误帧头, 服务端因此关闭连接")
		return
	}

	logf("第 %d/%d 步: 读取失败: %v", step, packetsPerClient, err)
}

func dialWithRetry(port int) (net.Conn, error) {
	var lastErr error

	// server 是异步启动的, 这里等它 listen 起来, 比固定 sleep 稳定
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
		if err == nil {
			return conn, nil
		}

		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}

	return nil, lastErr
}

// 把帧打印成一行可读的字段, 而不是 %v 打出来的结构体和字节切片
func formatFrame(p *tcp_fixed_head.ProtocolData) string {
	return fmt.Sprintf("version=0x%04X action=%s len=%d data=%q",
		p.Version, tcp_fixed_head.ActionName(p.ActionType), p.DataLength, string(p.Data))
}

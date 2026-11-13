package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
)

// websocket 服务的脚本化客户端, 用来验证:
//   - 握手成功, 普通文本消息能原路 echo 回来
//   - 一条消息被拆成多个 TCP 段(手工分片写)也能正确重组
//   - 多连接并发收发
//   - 客户端 close 握手后正常退出
//
// 默认会打印每一收每一发, 所以只看终端就能知道每步是否成功:
//
//	go run ./examples/websocket_client -addr ws://127.0.0.1:8081 -conns 2 -msgs 2
//	[client-0] CONNECT ws://127.0.0.1:8081
//	[client-0] OPEN    handshake ok, 耗时 1.2ms
//	[client-0] SPLIT   "client-0 split": 手工分 2 段写帧, echo 校验通过
//	[client-0] SEND    msg-0 "client-0 msg-0"
//	[client-0] RECV    msg-0 "response is :client-0 msg-0, 当前时间:..."
//	[client-0] CLOSE   正常关闭 (status=1000)
func main() {
	addr := flag.String("addr", "ws://127.0.0.1:8081", "websocket server url")
	conns := flag.Int("conns", 8, "concurrent connections")
	msgs := flag.Int("msgs", 5, "text messages per connection")
	splitFrames := flag.Bool("split", true, "send frames split across TCP segments first")
	verbose := flag.Bool("v", true, "log every send/receive; -v=false only prints the summary")
	flag.Parse()

	if *verbose {
		log.SetFlags(0) // 去掉时间前缀, 19 个 client 并发时一行更好读
	}

	var failed int64
	var wg sync.WaitGroup

	for i := 0; i < *conns; i++ {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			if err := runConn(*addr, idx, *msgs, *splitFrames, *verbose); err != nil {
				atomic.AddInt64(&failed, 1)
				log.Printf("[client-%d] FAILED: %+v", idx, err)
			}
		}(i)
	}

	wg.Wait()

	ok := int64(*conns) - atomic.LoadInt64(&failed)
	fmt.Printf("ok=%d fail=%d\n", ok, atomic.LoadInt64(&failed))

	if failed > 0 {
		os.Exit(1)
	}
}

func runConn(addr string, idx, msgs int, splitFrames, verbose bool) error {
	tag := fmt.Sprintf("[client-%d]", idx)
	logf := func(format string, args ...interface{}) {
		if verbose {
			log.Printf(tag+" "+format, args...)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	logf("CONNECT %s", addr)
	start := time.Now()

	wsConn, resp, err := coderws.Dial(ctx, addr, &coderws.DialOptions{
		HTTPClient: &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	logf("OPEN    handshake ok, status=%d, 耗时 %s, subprotocol=%q",
		resp.StatusCode, time.Since(start).Round(time.Millisecond), wsConn.Subprotocol())

	defer func() {
		// 1000 = 正常关闭; 服务端空闲回收是 1001, 这里能看到区别
		if err := wsConn.Close(coderws.StatusNormalClosure, ""); err != nil {
			logf("CLOSE   关闭时返回错误: %+v", err)
			return
		}

		logf("CLOSE   正常关闭 (status=%d)", coderws.StatusNormalClosure)
	}()

	if splitFrames {
		// 分片用例要绕过 coder/websocket 的编码器, 单独开一条裸 TCP 连接
		payload := fmt.Sprintf("client-%d split", idx)
		splitStart := time.Now()

		if err := splitRoundTrip(addr, payload); err != nil {
			return fmt.Errorf("split round trip: %w", err)
		}

		logf("SPLIT   %q: 手工分 2 段写帧, echo 校验通过, 耗时 %s",
			payload, time.Since(splitStart).Round(time.Millisecond))
	}

	for i := 0; i < msgs; i++ {
		want := fmt.Sprintf("client-%d msg-%d", idx, i)

		if err := wsConn.Write(ctx, coderws.MessageText, []byte(want)); err != nil {
			return fmt.Errorf("write msg %d: %w", i, err)
		}

		logf("SEND    msg-%d %q", i, want)

		_, got, err := wsConn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read msg %d: %w", i, err)
		}

		if err := checkEcho(want, string(got)); err != nil {
			return fmt.Errorf("msg %d: %w", i, err)
		}

		logf("RECV    msg-%d %q", i, string(got))
	}

	logf("DONE    %d 条消息全部 echo 校验通过, 总耗时 %s",
		msgs, time.Since(start).Round(time.Millisecond))

	return nil
}

// splitRoundTrip 手工完成握手, 然后手工发一个被 TCP 分片的掩码 text 帧:
// 先写前 3 字节, 等 300ms 再写剩下部分.
// 旧实现(gobwas + 非阻塞 Conn.Read)会在这里报 short buffer 并失步.
func splitRoundTrip(wsURL, payload string) error {
	u, err := url.Parse(wsURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}

	netConn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer netConn.Close()

	_ = netConn.SetDeadline(time.Now().Add(10 * time.Second))

	if err := writeHandshake(netConn, u.Host); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	frame := maskClientTextFrame([]byte(payload))

	if _, err := netConn.Write(frame[:3]); err != nil {
		return fmt.Errorf("write head: %w", err)
	}

	time.Sleep(300 * time.Millisecond)

	if _, err := netConn.Write(frame[3:]); err != nil {
		return fmt.Errorf("write body: %w", err)
	}

	got, err := readServerTextFrame(netConn)
	if err != nil {
		return fmt.Errorf("read echo: %w", err)
	}

	return checkEcho(payload, got)
}

func writeHandshake(conn net.Conn, host string) error {
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}

	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		host, base64.StdEncoding.EncodeToString(key[:]))

	if _, err := conn.Write([]byte(req)); err != nil {
		return err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("unexpected handshake status: %s", resp.Status)
	}

	return nil
}

// readServerTextFrame 读一个服务端发来的(未掩码的)完整 text 帧
func readServerTextFrame(conn net.Conn) (string, error) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", err
	}

	if head[0]&0x0F != 0x1 {
		return "", fmt.Errorf("unexpected opcode: %x", head[0]&0x0F)
	}
	if head[1]&0x80 != 0 {
		return "", fmt.Errorf("server frame must not be masked")
	}

	length := int(head[1] & 0x7F)
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(conn, ext); err != nil {
			return "", err
		}
		length = int(ext[0])<<8 | int(ext[1])

	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(conn, ext); err != nil {
			return "", err
		}
		length = int(binary.BigEndian.Uint64(ext))
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return "", err
	}

	return string(payload), nil
}

// echo 的格式固定为 "response is :<payload>, 当前时间:<ts>\n"
func checkEcho(want, got string) error {
	if len(got) < len(want) || got[:len("response is :")+len(want)] != "response is :"+want {
		return fmt.Errorf("unexpected echo: %q", got)
	}

	return nil
}

// maskClientTextFrame 生成一个单帧、fin、带掩码的 text 帧
func maskClientTextFrame(payload []byte) []byte {
	const (
		opText  = 0x1
		finBit  = 0x80
		maskBit = 0x80
		maskKey = 0x37
	)

	frame := make([]byte, 0, len(payload)+8)
	frame = append(frame, finBit|opText)

	switch n := len(payload); {
	case n < 126:
		frame = append(frame, maskBit|byte(n))
	case n <= 0xFFFF:
		frame = append(frame, maskBit|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, maskBit|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}

	var key [4]byte
	for i := range key {
		key[i] = maskKey
	}
	frame = append(frame, key[:]...)

	for i, b := range payload {
		frame = append(frame, b^key[i%4])
	}

	return frame
}

package websocket

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/panjf2000/gnet/v2"
	"github.com/pkg/errors"

	"github.com/cclehui/server_on_gnet/commonutil"
)

// WSConn 是 gnet 连接和 coder/websocket 之间的桥:
//   - reactor(OnTraffic) 只做一次拷贝, 把字节放进 inbound, 不碰任何协议解析;
//   - 每条连接一个读协程, 在 inbound 上做阻塞读, 由 coder/websocket 解帧;
//   - 写统一走 AsyncWrite(在 gnet 事件循环里执行), 所以任意协程都可以发消息.
//
// 为什么不能像 gobwas/ws 那样直接在 reactor 里解帧: coder/websocket 的握手和读帧
// 都是阻塞式 io, 而 gnet 的 Conn.Read 是非阻塞的(缓冲区空了立刻返回
// io.ErrShortBuffer), 一旦帧被 TCP 分片, io.ReadFull 就会读失败, 帧头随之丢失,
// 下一次解析从帧中间开始. 读协程 + 阻塞 io 才是这个库的用法.
type WSConn struct {
	// LastActiveTs 必须是第一个字段: 32 位平台上 atomic 访问 int64 要求 8 字节对齐.
	// 只允许用 atomic.LoadInt64 / atomic.StoreInt64 读写.
	LastActiveTs int64

	GnetConn gnet.Conn

	UniqId string //连接的全局唯一id ?

	inbound *inboundBuffer

	localAddr  net.Addr
	remoteAddr net.Addr

	// wsConn 由读协程在握手成功后发布, 其它协程通过 coderConn() 读取.
	wsConnMu sync.RWMutex
	wsConn   *coderws.Conn
}

// 新建一条 ws 连接(此时还没握手)
func NewWSConn(conn gnet.Conn) *WSConn {
	return &WSConn{
		GnetConn: conn,

		inbound: newInboundBuffer(),

		// LocalAddr/RemoteAddr 只能在事件循环里取, 这里拷一份给读协程用
		localAddr:  conn.LocalAddr(),
		remoteAddr: conn.RemoteAddr(),
	}
}

// inboundBuffer 是 reactor 和读协程之间的字节队列.
// reactor 只 Append, 读协程只 Read; Read 在没数据时阻塞等待而不是返回 short buffer.
type inboundBuffer struct {
	mu     sync.Mutex
	buf    []byte
	off    int
	closed bool
	// ready 只用来唤醒阻塞的 Read, cap 1, 发送永远不阻塞
	ready chan struct{}

	readDeadline time.Time
}

func newInboundBuffer() *inboundBuffer {
	return &inboundBuffer{ready: make(chan struct{}, 1)}
}

// 拷贝 p 入队. p 是 gnet 的借用缓冲区, 必须在事件循环里复制完.
func (b *inboundBuffer) Append(p []byte) {
	if len(p) == 0 {
		return
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.buf = append(b.buf, p...)
	b.mu.Unlock()

	b.wake()
}

// Buffered 当前待读字节数
func (b *inboundBuffer) Buffered() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.buf) - b.off
}

// Close 关闭队列并唤醒阻塞中的 Read, 之后 Read 返回 io.EOF
func (b *inboundBuffer) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	b.wake()
}

func (b *inboundBuffer) wake() {
	select {
	case b.ready <- struct{}{}:
	default:
	}
}

func (b *inboundBuffer) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		b.mu.Lock()
		if n := len(b.buf) - b.off; n > 0 {
			n = copy(p, b.buf[b.off:])
			b.off += n
			if b.off == len(b.buf) {
				// 读空了就复用底层数组, 但别把一次大包申请的几 MB 永久挂在连接上
				if cap(b.buf) > inboundBufferKeepCap {
					b.buf = nil
				} else {
					b.buf = b.buf[:0]
				}
				b.off = 0
			}
			b.mu.Unlock()

			return n, nil
		}

		if b.closed {
			b.mu.Unlock()
			return 0, io.EOF
		}

		deadline := b.readDeadline
		b.mu.Unlock()

		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			remain := time.Until(deadline)
			if remain <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(remain)
			timeout = timer.C
		}

		select {
		case <-b.ready:
		case <-timeout:
			return 0, os.ErrDeadlineExceeded
		}

		if timer != nil {
			timer.Stop()
		}
	}
}

func (b *inboundBuffer) SetReadDeadline(t time.Time) error {
	b.mu.Lock()
	b.readDeadline = t
	b.mu.Unlock()

	// 唤醒正在等待的 Read, 让它重新按新 deadline 计算
	b.wake()

	return nil
}

func (u *WSConn) appendInbound(p []byte) { u.inbound.Append(p) }

// InboundBuffered 还没被读协程消费的字节数, 只在事件循环里调用
func (u *WSConn) InboundBuffered() int { return u.inbound.Buffered() }

// 更新连接活跃时间到当前时间
func (u *WSConn) UpdateActiveTsToNow() {
	atomic.StoreInt64(&u.LastActiveTs, time.Now().Unix())
}

// 读缓存的保留下限, 超过这个容量的空缓冲区直接释放
const inboundBufferKeepCap = 64 << 10

// 下面这几个方法让 *WSConn 满足 net.Conn, coder/websocket 依赖它们.
// Read 只在读协程里调用; 其余方法(coder/websocket 会在写协程/Close 里调)必须并发安全.

func (u *WSConn) Read(p []byte) (int, error) { return u.inbound.Read(p) }

// Write 把数据拷贝后交给 gnet 的事件循环写出去.
// AsyncWrite 里的 buffer 要等事件循环执行时才被使用, 所以必须先拷贝.
func (u *WSConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	buf := make([]byte, len(p))
	copy(buf, p)

	if err := u.GnetConn.AsyncWrite(buf, nil); err != nil {
		return 0, errors.Wrap(err, "async write websocket frame")
	}

	return len(p), nil
}

// Close 同时关掉读队列(唤醒阻塞的读协程)和底层连接.
// coder/websocket 在写超时等场景会直接调它, 所以这里必须幂等且并发安全:
// AsyncWrite 在 gnet 里就是一次 Task 提交, gnet 自己处理已关闭的连接.
func (u *WSConn) Close() error {
	u.inbound.Close()

	return u.GnetConn.Close()
}

func (u *WSConn) LocalAddr() net.Addr  { return u.localAddr }
func (u *WSConn) RemoteAddr() net.Addr { return u.remoteAddr }

func (u *WSConn) SetDeadline(t time.Time) error {
	_ = u.SetWriteDeadline(t)

	return u.SetReadDeadline(t)
}

func (u *WSConn) SetReadDeadline(t time.Time) error { return u.inbound.SetReadDeadline(t) }

// 下行写是排到 gnet 事件循环里的, 没有可以设置的写超时;
// coder/websocket 也不依赖 deadline(它用 context 控制超时), 所以这里是空实现.
func (u *WSConn) SetWriteDeadline(t time.Time) error { return nil }

// coderConn 返回握手成功后的协议连接, 未握手时是 nil
func (u *WSConn) coderConn() *coderws.Conn {
	u.wsConnMu.RLock()
	defer u.wsConnMu.RUnlock()

	return u.wsConn
}

func (u *WSConn) setCoderConn(wsConn *coderws.Conn) {
	u.wsConnMu.Lock()
	u.wsConn = wsConn
	u.wsConnMu.Unlock()
}

// WriteMessage 发送一条消息. 可以在业务协程里调用(coder/websocket 的写是并发安全的).
func (u *WSConn) WriteMessage(msgType coderws.MessageType, data []byte) error {
	wsConn := u.coderConn()
	if wsConn == nil {
		return ErrNotUpgraded
	}

	// 对端不读时不能让业务协程无限等下去: 超时后 coder/websocket 会关掉连接
	ctx, cancel := context.WithTimeout(context.Background(), SEND_TIMEOUT)
	defer cancel()

	if err := wsConn.Write(ctx, msgType, data); err != nil {
		return errors.Wrap(err, "write websocket message")
	}

	return nil
}

// handshakeResponseWriter 让 coder/websocket.Accept 能在一个 gnet 连接上完成
// HTTP 101 握手: Accept 需要 http.ResponseWriter + http.Hijacker.
type handshakeResponseWriter struct {
	conn        *WSConn
	br          *bufio.Reader
	header      http.Header
	wroteHeader bool
}

func (w *handshakeResponseWriter) Header() http.Header { return w.header }

func (w *handshakeResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode))
	_ = w.header.Write(&b)
	b.WriteString("\r\n")

	if _, err := w.conn.Write(b.Bytes()); err != nil {
		commonutil.GetLogger().Errorf(context.Background(),
			"[server] 写 ws 握手响应失败, from:%s, err:%+v", w.conn.RemoteAddr(), err)
	}
}

func (w *handshakeResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.conn.Write(p)
}

// Hijack 交出底层连接. Accept 会 Peek 掉 br 里已经缓冲的剩余字节(客户端把首帧
// 和握手请求放在同一个包里时会走到这里), 所以 br 必须就是解析请求用的那一个.
func (w *handshakeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	bw := bufio.NewWriterSize(w.conn, HANDSHAKE_BUFFER_SIZE)

	return w.conn, bufio.NewReadWriter(w.br, bw), nil
}

// accept 在读协程里完成握手并返回 coder/websocket 连接
func (server *WebSocketServer) accept(wsConn *WSConn) (*coderws.Conn, error) {
	br := bufio.NewReaderSize(wsConn, HANDSHAKE_BUFFER_SIZE)

	req, err := http.ReadRequest(br)
	if err != nil {
		return nil, errors.Wrap(err, "read websocket handshake request")
	}

	rw := &handshakeResponseWriter{conn: wsConn, br: br, header: http.Header{}}
	// 保留原来的握手响应头
	rw.header.Set(GO_VERSION_HEADER_KEY, runtime.Version())

	wsConnProto, err := coderws.Accept(rw, req, server.AcceptOptions)
	if err != nil {
		return nil, errors.Wrap(err, "accept websocket")
	}

	return wsConnProto, nil
}

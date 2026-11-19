package mqtt

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/gnet/v2"
)

// Session 是一条已完成 CONNECT 的连接, 也是分发时的投递目标.
//
// 设计取舍: 只支持 clean session = 1 的语义(离线消息不排队、断连即清订阅).
// 保留离线会话要持久化订阅集合 + 未确认消息, 那需要存储层, 而这个仓库
// 刻意不带数据库(见 AGENTS.md 的 Environment 一节). 因此 CONNECT 里
// cleanSession=0 也会被接受, 但只在连接存活期间有效, 并通过 RetainSession
// 选项向对端说明这一限制.
type Session struct {
	ClientID string

	// Conn 是 gnet 连接. 只有 AsyncWrite / Close 可以跨协程调用,
	// 这里所有写回都走 AsyncWrite.
	Conn gnet.Conn

	// remoteAddr 在构造时(仍在 event loop 里)取成字符串快照.
	// gnet 的 Conn.RemoteAddr 是 event-loop-only, 而且连接被 release 之后
	// 再取会和回收过程构成 data race(-race 已抓到过一次), 所以日志一律用这个字段.
	remoteAddr string

	KeepAlive uint16

	// will 消息在 CONNECT 里声明, 非正常断开时由 OnClose 发布.
	// willMu 保护: OnClose 在 event loop, takeWill 也可能来自其他路径.
	willMu      sync.Mutex
	willTopic   string
	willPayload []byte
	willRetain  bool

	// lastActiveMillis 由 reactor 在每次收到完整包时更新, 维护协程读取.
	// 用 atomic 而不是锁: 读非常频繁(每秒扫一遍全部连接), 写只在收包时.
	lastActiveMillis int64

	closed int32
}

func newSession(clientID string, conn gnet.Conn, keepAlive uint16) *Session {
	session := &Session{
		ClientID:         clientID,
		Conn:             conn,
		KeepAlive:        keepAlive,
		lastActiveMillis: time.Now().UnixMilli(),
	}

	if conn != nil && conn.RemoteAddr() != nil {
		session.remoteAddr = conn.RemoteAddr().String()
	}

	return session
}

func (s *Session) touch() {
	atomic.StoreInt64(&s.lastActiveMillis, time.Now().UnixMilli())
}

// IsExpired 判断连接是否已经超过 1.5 倍 keepalive 没发任何包.
func (s *Session) IsExpired(nowMillis int64) bool {
	return isKeepAliveExpired(s.KeepAlive, nowMillis-atomic.LoadInt64(&s.lastActiveMillis))
}

func (s *Session) setWill(topic string, payload []byte, retain bool) {
	s.willMu.Lock()
	defer s.willMu.Unlock()

	s.willTopic = topic
	s.willPayload = payload
	s.willRetain = retain
}

// clearWill 在正常 DISCONNECT 时清空: 规范里客户端正常断开不触发 will.
func (s *Session) clearWill() {
	s.willMu.Lock()
	defer s.willMu.Unlock()

	s.willTopic = ""
	s.willPayload = nil
	s.willRetain = false
}

// takeWill 取走 will 并清空, 保证同一条连接只发布一次.
func (s *Session) takeWill() (topic string, payload []byte, retain bool, ok bool) {
	s.willMu.Lock()
	defer s.willMu.Unlock()

	if s.willTopic == "" {
		return "", nil, false, false
	}

	topic, payload, retain = s.willTopic, s.willPayload, s.willRetain
	s.willTopic, s.willPayload, s.willRetain = "", nil, false

	return topic, payload, retain, true
}

// write 回一帧控制包(CONNACK/SUBACK/UNSUBACK/PINGRESP).
// 只在 reactor 协程里调用, 但统一走 AsyncWrite: 和 deliver 共用一条写路径,
// 并避免 reactor 里的同步写被对端不读的场景卡住.
func (s *Session) write(frame []byte) error {
	return s.Conn.AsyncWrite(frame, nil)
}

// deliver 把一条 PUBLISH 写回该连接. 从任意协程调用, 只能 AsyncWrite.
// 返回非 nil 表示这条消息没送出去(连接已关/写失败), 由调用方记日志.
func (s *Session) deliver(topic string, payload []byte, retain bool) error {
	if atomic.LoadInt32(&s.closed) != 0 {
		return ErrSessionClosed
	}

	frame := EncodePublish(topic, payload, retain)

	return s.Conn.AsyncWrite(frame, nil)
}

// SessionRegistry 是 client id -> Session 的索引, 用于分发和解绑.
//
// 为什么需要它: 分发只靠订阅树拿到 client id, 还需要一个 client id 到连接的
// 反查; 这一层就是架构文档里说的 "connection registry", 单独存在是为了
// 让订阅树只管匹配、注册表只管连接生命周期.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{sessions: make(map[string]*Session)}
}

// Add 注册连接. sameID 为旧连接时会被关闭 —— 规范允许 client id 接管,
// 老连接继续存在会让同一个 client id 对应两个连接, 分发时状态不一致.
func (registry *SessionRegistry) Add(session *Session) (replaced *Session) {
	registry.mu.Lock()
	replaced = registry.sessions[session.ClientID]
	registry.sessions[session.ClientID] = session
	registry.mu.Unlock()

	if replaced != nil && replaced != session {
		replaced.markClosed()

		// Close 是并发安全的, 会投递到旧连接自己的 event loop
		_ = replaced.Conn.Close()
	}

	return replaced
}

// Get 反查连接. 分发时每条消息都会调用, 用读锁.
func (registry *SessionRegistry) Get(clientID string) *Session {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	return registry.sessions[clientID]
}

// Remove 解绑. 只有当前登记的就是这条 session 时才删, 避免把接管的新连接删掉.
func (registry *SessionRegistry) Remove(session *Session) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if registry.sessions[session.ClientID] != session {
		return false
	}

	delete(registry.sessions, session.ClientID)
	session.markClosed()

	return true
}

// Snapshot 返回当前全部连接的副本, 给维护协程做 keepalive 扫描用.
// 不能把内部 map 交出去: 扫描期间有连接进出会构成 map 并发读写.
func (registry *SessionRegistry) Snapshot() []*Session {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	sessions := make([]*Session, 0, len(registry.sessions))
	for _, session := range registry.sessions {
		sessions = append(sessions, session)
	}

	return sessions
}

func (registry *SessionRegistry) Len() int {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	return len(registry.sessions)
}

func (s *Session) markClosed() {
	atomic.StoreInt32(&s.closed, 1)
}

func (s *Session) isClosed() bool {
	return atomic.LoadInt32(&s.closed) != 0
}

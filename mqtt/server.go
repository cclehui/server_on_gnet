package mqtt

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"github.com/pkg/errors"
	"golang.org/x/net/context"

	"github.com/cclehui/server_on_gnet/commonutil"
)

// MQTTServer 是 MQTT 3.1.1 + QoS 0 的 broker 实现(gnet EventHandler).
//
// 支持: CONNECT/CONNACK, SUBSCRIBE/SUBACK(含 + / # 通配), UNSUBSCRIBE/UNSUBACK,
// PUBLISH(QoS 0 收发), PINGREQ/PINGRESP, DISCONNECT, retain 消息, will 消息,
// keepalive 超时回收, client id 接管.
//
// 不支持(都会在协议层明确拒绝或忽略, 不会静默降级):
//   - QoS 1/2: 收到 QoS>0 的 PUBLISH 直接关连接, 因为回不出 PUBACK/PUBREC
//   - 离线会话/消息排队: 断连即清订阅(见 Session 注释)
//   - 3.1("MQIsdp"/level 3) 与 MQTT 5: CONNACK 返回 0x01
//   - TLS/ACL: 没有监听层的 TLS; 账号密码只解析出来交给业务判断, 不做内置校验
type MQTTServer struct {
	// ProtoAddr 是 gnet 监听地址, 形如 "tcp://:1883"
	ProtoAddr string

	WorkerPool *ants.Pool

	// ConnNum 只由 reactor 用 atomic 修改, 外部读 GetConnNum()
	ConnNum int32

	Handler ServerHandler

	// acceptPersistentSessions 见 RetainSession 选项
	acceptPersistentSessions bool

	// 订阅树 / 连接注册表 / 保留消息是跨连接状态, 由 broker 自己持有.
	// 开了 multicore 后多个 event loop 会并发访问, 三者各自带锁.
	subscriptions *SubscriptionTree
	sessions      *SessionRegistry
	retained      *retainedStore

	// preAuth 记录"已建立 TCP 连接但还没发 CONNECT"的连接及建立时间.
	// 不跟踪的话, 一个连上来不发数据的客户端能永久占住一条连接和它的
	// event loop 上下文; ConnectTimeout 就是这条路径的上限.
	preAuthMu sync.Mutex
	preAuth   map[gnet.Conn]preAuthConn

	gnetOptions []gnet.Option

	// Engine 只在 OnBoot 里拿得到, 用 channel 交给信号协程(同 tcp_fixed_head)
	engineCh   chan gnet.Engine
	engineOnce sync.Once

	ctx context.Context
}

// Run 启动服务. 监听地址和引擎选项都已在构造期配好, 这里不接地址.
func (server *MQTTServer) Run() error {
	if server.Handler == nil || server.WorkerPool == nil || server.subscriptions == nil {
		return errors.New("mqtt: server not constructed by NewMQTTServer")
	}

	if server.ProtoAddr == "" {
		return errors.New("mqtt: Run needs a listen address (ProtoAddr / WithProtoAddr)")
	}

	go server.handleSignals()
	go server.maintainSessions()

	return gnet.Run(server, server.ProtoAddr, server.gnetOptions...)
}

// handleSignals 拿到 Engine 后按信号优雅停机. 用 Engine.Stop 而不是已 deprecated
// 的包级 gnet.Stop(后者按地址查全局表, 同地址重启会停错 engine).
func (server *MQTTServer) handleSignals() {
	osSignal := make(chan os.Signal, 1)
	signal.Notify(osSignal, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(osSignal)

	for s := range osSignal {
		commonutil.GetLogger().Infof(server.ctx, "[server] 收到系统信号:%s", s.String())

		engine, ok := <-server.engineCh
		if !ok {
			return
		}

		if err := engine.Stop(server.ctx); err != nil {
			commonutil.GetLogger().Errorf(server.ctx, "[server] Stop error:%+v", err)
		}

		return
	}
}

// maintainSessions 周期扫描 keepalive 超时的连接并断开.
// 放在普通 goroutine 而不是 OnTick: 本仓库没有 WithTicker, OnTick 不会被调用.
func (server *MQTTServer) maintainSessions() {
	ticker := time.NewTicker(MaintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-server.ctx.Done():
			return
		case now := <-ticker.C:
			server.closeStalePreAuth(now)
			for _, session := range server.expiredSessions(now.UnixMilli()) {
				commonutil.GetLogger().Infof(server.ctx,
					"[server] keepalive 超时, 断开连接, clientId:%q, from:%s, keepalive:%ds",
					session.ClientID, session.remoteAddr, session.KeepAlive)

				// 从注册表摘掉并关连接; 之后 reactor 里的 OnClose 仍会跑,
				// Remove 对同一条 session 是幂等的
				server.sessions.Remove(session)
				_ = session.Conn.Close()
			}
		}
	}
}

func (server *MQTTServer) expiredSessions(nowMillis int64) []*Session {
	expired := make([]*Session, 0)

	for _, session := range server.sessions.Snapshot() {
		if session.IsExpired(nowMillis) {
			expired = append(expired, session)
		}
	}

	return expired
}

// preAuthConn 记录"已连上但还没 CONNECT"的连接.
// 地址在 OnOpen 里(仍在 event loop)就取成字符串, 维护协程只读快照 ——
// gnet 的 Conn.RemoteAddr 是 event-loop-only, 跨协程读会和连接回收竞争.
type preAuthConn struct {
	openedAt   time.Time
	remoteAddr string
}

// closeStalePreAuth 关掉连上后迟迟不发 CONNECT 的连接.
func (server *MQTTServer) closeStalePreAuth(now time.Time) {
	stale := make([]gnet.Conn, 0)
	staleAddrs := make([]string, 0)

	server.preAuthMu.Lock()
	for conn, meta := range server.preAuth {
		if now.Sub(meta.openedAt) > ConnectTimeout {
			stale = append(stale, conn)
			staleAddrs = append(staleAddrs, meta.remoteAddr)
		}
	}

	for _, conn := range stale {
		delete(server.preAuth, conn)
	}
	server.preAuthMu.Unlock()

	for i, conn := range stale {
		commonutil.GetLogger().Infof(server.ctx,
			"[server] %s 内未收到 CONNECT, 断开连接: %s", ConnectTimeout, staleAddrs[i])

		_ = conn.Close()
	}
}

// markConnected 在 CONNECT 成功后把连接从 preAuth 名单里摘掉.
func (server *MQTTServer) markConnected(c gnet.Conn) {
	server.preAuthMu.Lock()
	delete(server.preAuth, c)
	server.preAuthMu.Unlock()
}

// GetConnNum 当前 TCP 连接数. ConnNum 被 reactor 用 atomic 改, 必须走这个 getter.
func (server *MQTTServer) GetConnNum() int32 {
	return atomic.LoadInt32(&server.ConnNum)
}

// GetSessionNum 已 CONNECT 成功的会话数, 和 TCP 连接数不同:
// 连接可以先于 CONNECT 存在, 也可能 CONNECT 后被同 client id 接管.
func (server *MQTTServer) GetSessionNum() int {
	return server.sessions.Len()
}

func (server *MQTTServer) OnBoot(eng gnet.Engine) (action gnet.Action) {
	if server.engineCh != nil {
		server.engineCh <- eng
	}

	commonutil.GetLogger().Infof(server.ctx, "[server] mqtt server 监听在 %s", server.ProtoAddr)

	return
}

func (server *MQTTServer) OnShutdown(eng gnet.Engine) {
	server.closeEngineCh()

	commonutil.GetLogger().Infof(server.ctx, "[server] mqtt server shutdown on %s", server.ProtoAddr)
}

func (server *MQTTServer) closeEngineCh() {
	server.engineOnce.Do(func() {
		close(server.engineCh)
	})
}

func (server *MQTTServer) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	totalNum := atomic.AddInt32(&server.ConnNum, 1)

	remoteAddr := "unknown"
	if c.RemoteAddr() != nil {
		remoteAddr = c.RemoteAddr().String()
	}

	server.preAuthMu.Lock()
	server.preAuth[c] = preAuthConn{openedAt: time.Now(), remoteAddr: remoteAddr}
	server.preAuthMu.Unlock()

	commonutil.GetLogger().Infof(server.ctx,
		"[server] 连接数=%d, 新连接: %s", totalNum, c.RemoteAddr())

	return
}

func (server *MQTTServer) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	atomic.AddInt32(&server.ConnNum, -1)

	server.preAuthMu.Lock()
	delete(server.preAuth, c)
	server.preAuthMu.Unlock()

	// 没发 CONNECT 就断开的连接没有 Session, 只回收计数
	session, ok := c.Context().(*Session)
	if !ok || session == nil {
		commonutil.GetLogger().Debugf(server.ctx, "[server] 关闭连接: %s", c.RemoteAddr())

		return
	}

	// 只有注册表里登记的还是这条连接时才清订阅: 被接管的老连接断开时
	// 不能把新连接的订阅一起删掉.
	if server.sessions.Remove(session) {
		removed := server.subscriptions.RemoveClient(session.ClientID)
		commonutil.GetLogger().Infof(server.ctx,
			"[server] 清理会话, clientId:%q, 删除订阅数:%d", session.ClientID, removed)
	}

	// will 的发布条件和"是否还登记在注册表"无关: keepalive 超时和 client id
	// 接管都会先被维护协程/新连接 Remove 掉, 但按规范这两种都属于非正常断开,
	// will 仍要发. 正常 DISCONNECT 已在 handlePacket 里 clearWill, 这里 take 不到.
	if willTopic, willPayload, willRetain, ok := session.takeWill(); ok {
		commonutil.GetLogger().Infof(server.ctx,
			"[server] 发布 will 消息, clientId:%q, topic:%q, payload:%q",
			session.ClientID, willTopic, string(willPayload))

		_ = server.publish(willTopic, willPayload, willRetain, nil)
	}

	commonutil.GetLogger().Debugf(server.ctx, "[server] 关闭连接: %s", c.RemoteAddr())

	return
}

func (server *MQTTServer) OnTick() (delay time.Duration, action gnet.Action) {
	return
}

// OnTraffic 是 reactor 里做的事: 循环解帧, 处理协议状态机, 只把业务回调投给 worker.
//
// 为什么协议状态机不投 worker: 同一个连接上 CONNECT / SUBSCRIBE / PUBLISH 有严格
// 先后语义. ants 不保证多任务的执行顺序, 把 CONNECT 投进池里时紧随其后的
// SUBSCRIBE 可能先跑, 看到 "还没 CONNECT" 就把连接关掉. 这里的关键帧处理都是
// 内存操作(订阅树匹配 + AsyncWrite 入队), 没有阻塞 I/O, 符合"reactor 只做有界工作";
// 真正用户定义的 ServerHandler 仍然走 worker 池.
func (server *MQTTServer) OnTraffic(c gnet.Conn) (action gnet.Action) {
	for {
		packet, err := Decode(c)

		if err != nil {
			if err == ErrIncompletePacket {
				return gnet.None
			}

			// 协议错误直接关连接. 原因是 MQTT 没有通用的 NACK: CONNACK 只在
			// CONNECT 阶段合法, SUBACK 需要 packet id —— 帧可能已经错位, 拿不到.
			commonutil.GetLogger().Errorf(server.ctx,
				"[server] 解包失败, 关闭连接:%+v, from:%s", err, c.RemoteAddr())

			return gnet.Close
		}

		if err := server.handlePacket(c, packet); err != nil {
			commonutil.GetLogger().Errorf(server.ctx,
				"[server] 处理 %s 失败, 关闭连接:%+v, from:%s",
				PacketTypeName(packet.Type), err, c.RemoteAddr())

			return gnet.Close
		}
	}
}

// handlePacket 在 reactor 协程里执行协议状态机. 返回非 nil 表示必须关闭连接.
func (server *MQTTServer) handlePacket(c gnet.Conn, packet *Packet) error {
	session, _ := c.Context().(*Session)

	// CONNECT 是唯一允许在 session 建立前到达的包
	if packet.Type == PACKET_CONNECT {
		if session != nil {
			return ErrSecondConnect
		}

		return server.handleConnect(c, packet)
	}

	if session == nil {
		return ErrSessionNotEstablished
	}

	// 收到任何完整控制包都算活跃, keepalive 是"有没有流量", 不是"有没有 PING"
	session.touch()

	switch packet.Type {
	case PACKET_PUBLISH:
		// 先分发再回调业务: 分发是 QoS 0 的协议语义, 业务慢或报错不应该
		// 让订阅者收不到消息.
		if err := server.publish(packet.Topic, packet.Payload, packet.Retain, session); err != nil {
			return err
		}

		handlerCtx := &HandlerContext{
			Packet:  packet,
			Session: session,
			server:  server,
		}

		// 唯一进池的部分: 用户业务. Submit 失败(doc: Nonblocking 模式下池满)
		// 不能影响协议, 但要留下线索, 否则表现为"业务回调偶尔丢".
		if submitErr := server.WorkerPool.Submit(func() {
			server.Handler(handlerCtx)
		}); submitErr != nil {
			commonutil.GetLogger().Warnf(server.ctx,
				"[server] worker 池已满, 丢弃业务回调, clientId:%q, topic:%q",
				session.ClientID, packet.Topic)
		}
	case PACKET_SUBSCRIBE:
		server.handleSubscribe(session, packet)
	case PACKET_UNSUBSCRIBE:
		server.handleUnsubscribe(session, packet)
	case PACKET_PINGREQ:
		return session.write(EncodePingresp())
	case PACKET_DISCONNECT:
		// 正常断开: 清 will, 让 OnClose 不再发 will; 订阅和会话由 OnClose 统一清理
		session.clearWill()

		return session.Conn.Close()
	case PACKET_PUBACK, PACKET_PUBREC, PACKET_PUBREL, PACKET_PUBCOMP, PACKET_UNSUBACK:
		// 我们不发 QoS>0 下行, 收到的确认包没有对应 inflight 记录, 忽略.
		// 记 Debug 是为了排查"对端以为自己在做 QoS 1"的误配.
		commonutil.GetLogger().Debugf(server.ctx,
			"[server] 忽略 %s, clientId:%q, packetId:%d",
			PacketTypeName(packet.Type), session.ClientID, packet.PacketID)
	default:
		commonutil.GetLogger().Debugf(server.ctx,
			"[server] 忽略 %s, clientId:%q", PacketTypeName(packet.Type), session.ClientID)
	}

	return nil
}

func (server *MQTTServer) handleConnect(c gnet.Conn, packet *Packet) error {
	if packet.ClientID == "" {
		// 3.1.1 允许空 client id, 但仅限 cleanSession=1; 用连接地址生成唯一 id,
		// 保证注册表和订阅树里的 key 不冲突.
		packet.ClientID = "auto-" + c.RemoteAddr().String()
	}

	if !packet.CleanSession && !server.acceptPersistentSessions {
		commonutil.GetLogger().Infof(server.ctx,
			"[server] 拒绝 cleanSession=0 的连接, clientId:%q: 本 broker 不保存离线会话",
			packet.ClientID)

		// 先回 CONNACK 再关: 只 Close 的话对端看到的是 reset, 不知道是协议拒绝
		if err := c.AsyncWrite(EncodeConnack(CONNACK_REFUSED_ID_REJECTED), nil); err != nil {
			return err
		}

		return errors.New("mqtt: persistent session rejected")
	}

	session := newSession(packet.ClientID, c, packet.KeepAlive)

	// cleanSession=1 表示不延续任何旧状态: 同 id 上次连接订阅的 filter 必须清掉,
	// 否则新连接会收到它从未订阅过的消息. cleanSession=0 时保留(不落盘, 重启仍会丢).
	if packet.CleanSession {
		if removed := server.subscriptions.RemoveClient(packet.ClientID); removed > 0 {
			commonutil.GetLogger().Infof(server.ctx,
				"[server] cleanSession=1, 丢弃旧订阅, clientId:%q, 删除订阅数:%d",
				packet.ClientID, removed)
		}
	}

	if packet.WillTopic != "" {
		session.setWill(packet.WillTopic, packet.WillPayload, packet.WillRetain)
	}

	// 先注册再回 CONNACK: 注册可能关掉同 id 的老连接, 新连接必须立即成为唯一 owner.
	// 注意用 replaced.remoteAddr 而不是 replaced.Conn.RemoteAddr(): Add 已经
	// 关掉旧连接, 再去读它的地址就是在和连接回收竞争.
	if replaced := server.sessions.Add(session); replaced != nil {
		commonutil.GetLogger().Infof(server.ctx,
			"[server] client id 接管, clientId:%q, 关闭旧连接:%s",
			packet.ClientID, replaced.remoteAddr)
	}

	// SetContext 只能在 event loop 里调 —— 这里正是 event loop
	c.SetContext(session)
	server.markConnected(c)

	if err := session.write(EncodeConnack(CONNACK_ACCEPTED)); err != nil {
		return err
	}

	commonutil.GetLogger().Infof(server.ctx,
		"[server] CONNECT 成功, clientId:%q, cleanSession:%v, keepalive:%ds, from:%s",
		packet.ClientID, packet.CleanSession, packet.KeepAlive, session.remoteAddr)

	return nil
}

func (server *MQTTServer) handleSubscribe(session *Session, packet *Packet) {
	codes := make([]byte, 0, len(packet.Subscriptions))

	for _, subscription := range packet.Subscriptions {
		// 请求 QoS 1/2 时按 QoS 0 授予: 连接还能用, 只是没有重传保证.
		// SUBACK 的 0x00 就是这个"降级授予"的正式回复.
		server.subscriptions.Subscribe(subscription.Filter, session.ClientID, 0)
		codes = append(codes, 0x00)

		commonutil.GetLogger().Infof(server.ctx,
			"[server] 订阅, clientId:%q, filter:%q, 请求qos:%d, 授予qos:0",
			session.ClientID, subscription.Filter, subscription.QoS)
	}

	if err := session.write(EncodeSuback(packet.PacketID, codes)); err != nil {
		commonutil.GetLogger().Errorf(server.ctx,
			"[server] 回写 SUBACK 失败, clientId:%q, err:%+v", session.ClientID, err)

		return
	}

	// 回放保留消息: 规范要求订阅建立后, 把匹配的 retain 消息按 PUBLISH(RETAIN=1) 发出.
	// 一次 SUBSCRIBE 里多个 filter 可能命中同一个 topic("a/#" 和 "a/b"),
	// 按 topic 去重, 否则同一条保留消息会重放多遍.
	replayed := make(map[string][]byte)
	for _, subscription := range packet.Subscriptions {
		for topic, payload := range server.retained.Matching(subscription.Filter) {
			replayed[topic] = payload
		}
	}

	for topic, payload := range replayed {
		if err := session.deliver(topic, payload, true); err != nil {
			// 对端刚订阅就断开时这里必然失败, 是 QoS 0 的正常结局, 不是服务端故障
			commonutil.GetLogger().Debugf(server.ctx,
				"[server] 回放保留消息失败, clientId:%q, topic:%q, err:%+v",
				session.ClientID, topic, err)
		}
	}
}

func (server *MQTTServer) handleUnsubscribe(session *Session, packet *Packet) {
	for _, filter := range packet.Filters {
		removed := server.subscriptions.Unsubscribe(filter, session.ClientID)

		commonutil.GetLogger().Infof(server.ctx,
			"[server] 取消订阅, clientId:%q, filter:%q, 命中:%v", session.ClientID, filter, removed)
	}

	if err := session.write(EncodeUnsuback(packet.PacketID)); err != nil {
		commonutil.GetLogger().Errorf(server.ctx,
			"[server] 回写 UNSUBACK 失败, clientId:%q, err:%+v", session.ClientID, err)
	}
}

// Publish 让业务侧以 broker 身份投递消息, 等价于某个客户端发布一条 QoS 0 消息.
// 可以从任意协程调用.
func (server *MQTTServer) Publish(topic string, payload []byte, retain bool) error {
	if err := ValidateTopicName(topic); err != nil {
		return err
	}

	return server.publish(topic, payload, retain, nil)
}

// publish 是 QoS 0 的核心路径: retain 落库 -> 订阅树匹配 -> 逐连接 AsyncWrite.
// publisher 为 nil 表示 broker 自己发的(或 will 消息), 不影响匹配逻辑.
//
// retain 只决定"是否更新保留消息", 不决定转发帧的 RETAIN 标志: 规范 3.3.1.3
// 要求对已建立订阅的转发一律 RETAIN=0, 只有订阅时回放的保留消息才是 RETAIN=1
// (在 handleSubscribe 里单独处理). 透传 retain 会让订阅者误以为每条实时消息
// 都是保留消息.
func (server *MQTTServer) publish(topic string, payload []byte, retain bool, publisher *Session) error {
	if retain {
		server.retained.Set(topic, payload)
	}

	// 匹配和注册表查询是两次独立的锁: 中间允许有连接进出,
	// 最坏情况是刚断开的连接少收一条消息, QoS 0 可以接受.
	matched := server.subscriptions.Match(topic)

	delivered := 0
	for clientID := range matched {
		session := server.sessions.Get(clientID)
		if session == nil || session.isClosed() {
			continue
		}

		// 规范: QoS 0 的 PUBLISH 也要回给发布者自己(如果它订阅了该 topic),
		// 所以这里不过滤 publisher; RETAIN 一律为 0(见函数注释).
		if err := session.deliver(topic, payload, false); err != nil {
			commonutil.GetLogger().Debugf(server.ctx,
				"[server] 分发失败, topic:%q, clientId:%q, err:%+v", topic, clientID, err)

			continue
		}

		delivered++
	}

	from := "broker"
	if publisher != nil {
		from = publisher.ClientID
	}

	commonutil.GetLogger().Infof(server.ctx,
		"[server] PUBLISH topic:%q, from:%q, 订阅者:%d, 已投递:%d, retain:%v, payload:%q",
		topic, from, len(matched), delivered, retain, string(payload))

	return nil
}

package main

import (
	"context"
	"flag"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/panjf2000/gnet/v2"

	"github.com/cclehui/server_on_gnet/commonutil"
	"github.com/cclehui/server_on_gnet/mqtt"
)

// MQTT 3.1.1 QoS 0 broker 示例.
//
// 客户端不再是手写的字节拼装, 而是用 eclipse/paho.mqtt.golang 的真实客户端:
// 服务端解出来的每一帧都经过 paho 的 packets 编解码, 客户端发出去/收回来的
// 也是 paho 自己的实现, 两边互为对照.
//
// 帧格式 (固定头 + varint 长度 + body):
//
//	byte0: packet type(高 4 位) | flags(低 4 位)
//	byte1..: remaining length (7 位一组, 最高位是续位, 最多 4 字节)
//	body:   随包类型变化; PUBLISH 是 topic(2B 长度 + UTF-8) + payload
//
// 示例覆盖:
//  1. 通配订阅: client-a 订阅 demo/+/temp, client-b 发布 demo/room1/temp 能收到;
//     client-a 另外订阅 demo/room1/humidity, demo/room2/humidity 不该命中
//  2. retain: client-c 用 retain=1 发布 demo/status, 晚到的 client-d 订阅即收到
//  3. 自发布回显: QoS 0 下客户端订阅自己的 topic 也会收到
//  4. QoS 1 被拒绝: 服务端只做 QoS 0, 收到 qos=1 的 PUBLISH 直接断开该连接
//
// go run ./examples/mqtt --port 11883
func main() {
	var port int
	var multicore bool

	flag.IntVar(&port, "port", 11883, "server port (1883 below 1024 needs root)")
	flag.BoolVar(&multicore, "multicore", true, "multicore")
	flag.Parse()

	ctx := context.Background()
	printBanner(port)

	server := mqtt.NewMQTTServer(
		mqtt.WithProtoAddr(fmt.Sprintf("tcp://:%d", port)),
		mqtt.WithContext(ctx),
		mqtt.WithGnetOptions(
			gnet.WithReusePort(true),
			gnet.WithMulticore(multicore),
			gnet.WithTCPKeepAlive(time.Second*90),
		),
	)

	clients := demoClients(port)
	brokerAddr := fmt.Sprintf("tcp://127.0.0.1:%d", port)

	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)

		client := client
		go func() {
			defer wg.Done()
			client.run(ctx, brokerAddr)
		}()
	}

	go func() {
		wg.Wait()

		commonutil.GetLogger().Infof(ctx, "[server] 演示结束: %d 个客户端均已退出", len(clients))

		// paho 的 Disconnect 返回不代表服务端 OnClose 已经跑完, 等连接真正回收
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			if server.GetConnNum() == 0 {
				break
			}

			time.Sleep(50 * time.Millisecond)
		}

		commonutil.GetLogger().Infof(ctx,
			"[server] 连接已回收: conns=%d sessions=%d (全部 DISCONNECT 后应为 0)",
			server.GetConnNum(), server.GetSessionNum())
	}()

	if err := server.Run(); err != nil {
		commonutil.GetLogger().Errorf(ctx, "启动失败:%+v", err)
	}
}

func printBanner(port int) {
	fmt.Println("================================================================")
	fmt.Println("mqtt 示例: MQTT 3.1.1 / QoS 0 broker (客户端用 eclipse/paho.mqtt.golang)")
	fmt.Printf("服务端: tcp://localhost:%d\n", port)
	fmt.Println("客户端:")
	fmt.Println("  client-a : subscribe demo/+/temp, demo/room1/humidity")
	fmt.Println("  client-b : publish demo/room1/temp (qos0), demo/room2/humidity (qos0)")
	fmt.Println("  client-c : publish demo/status (retain), 再 publish qos=1 触发断开")
	fmt.Println("  client-d : 晚于 client-c 订阅 demo/status, 应收到 retain 消息")
	fmt.Println("日志: [server] 是 broker, [client-x] 是客户端; grep 'server]' / grep 'client-'")
	fmt.Println("================================================================")
}

const (
	demoTopicTemp     = "demo/room1/temp"
	demoTopicHumidity = "demo/room2/humidity"
	demoTopicStatus   = "demo/status"
)

// demoClient 是一个脚本化客户端: 每个客户端有固定名字和一段自己的剧本.
type demoClient struct {
	name   string
	port   int
	script func(c *testClient, logf func(string, ...interface{}))
}

func demoClients(port int) []demoClient {
	return []demoClient{
		{name: "client-a", port: port, script: runSubscriber},
		{name: "client-b", port: port, script: runPublisher},
		{name: "client-c", port: port, script: runRetainAndBadQoS},
		{name: "client-d", port: port, script: runLateSubscriber},
	}
}

// run 建连并跑剧本. logf 统一打上 [client-N] 前缀, 和 [server] 日志区分.
func (d demoClient) run(ctx context.Context, brokerAddr string) {
	logf := func(format string, args ...interface{}) {
		all := make([]interface{}, 0, len(args)+1)
		all = append(all, d.name)
		all = append(all, args...)
		commonutil.GetLogger().Infof(ctx, "[%s] "+format, all...)
	}

	c := newTestClient(d.name, brokerAddr, logf)
	if err := c.connect(); err != nil {
		logf("连接失败: %v", err)

		return
	}

	defer c.close()

	logf("CONNECT 成功, 已连上 %s", brokerAddr)

	d.script(c, logf)
}

func runSubscriber(c *testClient, logf func(string, ...interface{})) {
	if err := c.subscribe("demo/+/temp", 0, "demo/room1/humidity", 0); err != nil {
		logf("订阅失败: %v", err)

		return
	}

	logf("已订阅 demo/+/temp 和 demo/room1/humidity, 等 client-b 发消息")

	// 期望收到 client-b 的温度消息
	for _, want := range []struct{ topic, payload string }{
		{demoTopicTemp, "23.5C"},
	} {
		msg, err := c.next(4 * time.Second)
		if err != nil {
			logf("等 %s 失败: %v", want.topic, err)

			return
		}

		logf("收到 topic=%q payload=%q (期望 %q)", msg.Topic(), string(msg.Payload()), want.payload)
	}

	// humidity 发在 demo/room2 下, 不在订阅范围内: 用一次短等待确认"没收到"
	if msg, err := c.next(700 * time.Millisecond); err == nil {
		logf("意外收到 topic=%q (本不该匹配 demo/room1/humidity)", msg.Topic())
	} else {
		logf("确认没有收到 demo/room2/humidity (通配符未误匹配)")
	}

	// 自己发布的 topic 也会回显: client-a 也发一条, 应该收到自己的消息
	if err := c.publish("demo/room1/temp", []byte("self-echo"), 0, false); err != nil {
		logf("自发布失败: %v", err)

		return
	}

	msg, err := c.next(2 * time.Second)
	if err != nil {
		logf("没收到自己发布的消息(疑似 QoS 0 回显缺失): %v", err)
	} else {
		logf("收到自己发布的回显 topic=%q payload=%q", msg.Topic(), string(msg.Payload()))
	}
}

func runPublisher(c *testClient, logf func(string, ...interface{})) {
	// 等 client-a 的 SUBACK 和 client-c 的 retain 发布先落定, 日志顺序更好读
	time.Sleep(500 * time.Millisecond)

	if err := c.publish(demoTopicTemp, []byte("23.5C"), 0, false); err != nil {
		logf("发布温度失败: %v", err)

		return
	}

	logf("已发布 %s = 23.5C", demoTopicTemp)

	// demo/room2/humidity 谁都没订阅, broker 会记"订阅者:0", 这是预期
	if err := c.publish(demoTopicHumidity, []byte("40%"), 0, false); err != nil {
		logf("发布湿度失败: %v", err)

		return
	}

	logf("已发布 %s = 40%% (无人订阅, 预期 broker 记 0 个订阅者)", demoTopicHumidity)

	time.Sleep(1500 * time.Millisecond)
}

func runRetainAndBadQoS(c *testClient, logf func(string, ...interface{})) {
	if err := c.publish(demoTopicStatus, []byte("online"), 0, true); err != nil {
		logf("发布 retain 消息失败: %v", err)

		return
	}

	logf("已用 retain=1 发布 %s = online (client-d 之后订阅时应该收到)", demoTopicStatus)

	// 给 client-d 留出订阅并收 retain 的时间, 再演示 QoS 1 被拒绝
	time.Sleep(2 * time.Second)

	logf("现在故意用 qos=1 发布 (服务端只支持 qos0, 预期被断开)")

	// paho 的 Publish 在 qos=1 下会等 PUBACK: 服务端直接断连, token 最终报错.
	// 这里用短超时, 免得 demo 挂在一个永远不会来的确认上.
	token := c.client.Publish(demoTopicTemp, 1, false, []byte("qos1-should-fail"))
	if !token.WaitTimeout(3 * time.Second) {
		logf("qos=1 的 Publish 没有在 3s 内返回 (服务端已断连, 符合预期)")
	} else if err := token.Error(); err != nil {
		logf("qos=1 的 Publish 报错, 符合预期: %v", err)
	} else {
		logf("意外: 服务端接受了 qos=1 的发布")
	}

	if c.isConnectedWithin(1500 * time.Millisecond) {
		logf("意外: 连接仍在")
	} else {
		logf("连接已被服务端断开, 符合预期")
	}
}

func runLateSubscriber(c *testClient, logf func(string, ...interface{})) {
	// 刻意晚于 client-c 的 retain 发布: 验证订阅时会回放保留消息
	time.Sleep(time.Second)

	if err := c.subscribe(demoTopicStatus, 0); err != nil {
		logf("订阅失败: %v", err)

		return
	}

	logf("已在 retain 发布之后订阅 %s", demoTopicStatus)

	msg, err := c.next(2 * time.Second)
	if err != nil {
		logf("没收到保留消息: %v", err)

		return
	}

	logf("收到保留消息 topic=%q payload=%q", msg.Topic(), string(msg.Payload()))

	if string(msg.Payload()) != "online" {
		logf("保留消息内容不对, 期望 online, 实际 %q", string(msg.Payload()))
	}
}

// ---- 下面是 paho 客户端的一层薄封装: 把"收到一条消息"变成一个可超时等待的
// 队列, 这样脚本可以写"确认 700ms 内没有收到"这种断言, 而不用 sleep 猜. ----

type testClient struct {
	name   string
	client paho.Client
	inbox  chan paho.Message
	logf   func(string, ...interface{})
	closed int32
}

func newTestClient(name, brokerAddr string, logf func(string, ...interface{})) *testClient {
	c := &testClient{
		name:  name,
		inbox: make(chan paho.Message, 64),
		logf:  logf,
	}

	options := paho.NewClientOptions().
		AddBroker(brokerAddr).
		SetClientID(name).
		SetCleanSession(true).
		SetKeepAlive(60 * time.Second).
		SetConnectTimeout(5 * time.Second).
		// demo 自己控制重连节奏: paho 默认会在断连后不停重试, 这里关掉,
		// 好让"服务端断开连接"这件事在日志里只出现一次.
		SetAutoReconnect(false).
		SetOrderMatters(false)

	options.SetConnectionLostHandler(func(_ paho.Client, err error) {
		atomic.StoreInt32(&c.closed, 1)
		logf("连接断开: %v", err)
	})

	options.SetOnConnectHandler(func(paho.Client) {
		atomic.StoreInt32(&c.closed, 0)
	})

	// 所有主题共用一个 handler: 脚本按自己的顺序从 inbox 取, 不需要按 topic 分流
	options.SetDefaultPublishHandler(func(_ paho.Client, msg paho.Message) {
		select {
		case c.inbox <- msg:
		default:
			// inbox 满说明 demo 脚本读得太慢, 丢一条不影响结论
		}
	})

	c.client = paho.NewClient(options)

	return c
}

func (c *testClient) connect() error {
	token := c.client.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("CONNECT 超时")
	}

	return token.Error()
}

func (c *testClient) subscribe(pairs ...interface{}) error {
	filters := make(map[string]byte)

	for i := 0; i+1 < len(pairs); i += 2 {
		filter, ok := pairs[i].(string)
		if !ok {
			return fmt.Errorf("subscribe 参数类型错误")
		}

		qos, ok := pairs[i+1].(int)
		if !ok {
			return fmt.Errorf("subscribe 参数类型错误")
		}

		filters[filter] = byte(qos)
	}

	token := c.client.SubscribeMultiple(filters, nil)
	if !token.WaitTimeout(3 * time.Second) {
		return fmt.Errorf("SUBACK 超时")
	}

	return token.Error()
}

func (c *testClient) publish(topic string, payload []byte, qos byte, retain bool) error {
	token := c.client.Publish(topic, qos, retain, payload)
	if !token.WaitTimeout(3 * time.Second) {
		return fmt.Errorf("publish 超时")
	}

	return token.Error()
}

// next 等一条消息, 超时返回错误. 用 channel + timer 而不是 sleep,
// 这样"确实没收到"和"收到得慢"能区分开.
func (c *testClient) next(timeout time.Duration) (paho.Message, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg := <-c.inbox:
		return msg, nil
	case <-timer.C:
		return nil, fmt.Errorf("%s 内没有收到消息", timeout)
	}
}

func (c *testClient) isConnectedWithin(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&c.closed) != 0 || !c.client.IsConnected() {
			return false
		}

		time.Sleep(50 * time.Millisecond)
	}

	return true
}

func (c *testClient) close() {
	if c.client.IsConnected() {
		// 正常 DISCONNECT: 服务端据此清 will、清订阅
		c.client.Disconnect(250)
	}
}

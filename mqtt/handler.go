package mqtt

import "github.com/cclehui/server_on_gnet/commonutil"

// ServerHandler 是业务处理入口, 只对收到 PUBLISH 时调用.
//
// 注意边界: 协议层的动作(CONNACK/SUBACK/PINGRESP、订阅树维护、按订阅分发)
// 由 broker 自己完成, 替换 Handler 不会让这些停止工作. Handler 拿到的是
// "已经路由完的那条应用消息", 用来做落库、桥接、审计、指标之类的事.
// 这也是它和 tcp_fixed_head 的区别: 那边 Handler 就是 echo 本身,
// 这边 echo 由协议语义(QoS 0 分发)决定, 不该由业务重复实现.
type ServerHandler func(ctx *HandlerContext)

// defaultHandler 只记日志: 分发已经由 server 完成, 默认不需要额外观测行为
var defaultHandler ServerHandler = func(ctx *HandlerContext) {
	commonutil.GetLogger().Debugf(ctx, "[server] 收到 PUBLISH, from:%s, topic:%q, payload:%q",
		ctx.Session.ClientID, ctx.Packet.Topic, string(ctx.Packet.Payload))
}

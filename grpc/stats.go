package grpc

import (
	"context"
	"sync/atomic"

	"github.com/cclehui/server_on_gnet/commonutil"
	"google.golang.org/grpc/stats"
)

type connTagKey struct{}

// connStatsHandler 在可选的自定义 handler 外面包一层连接计数.
// gRPC 没有 gnet 的 OnOpen/OnClose, ConnBegin/ConnEnd 是等价的连接生命周期事件.
type connStatsHandler struct {
	server *Server
	next   stats.Handler
}

func newConnStatsHandler(server *Server, next stats.Handler) stats.Handler {
	return &connStatsHandler{server: server, next: next}
}

func (handler *connStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	if handler.next != nil {
		return handler.next.TagRPC(ctx, info)
	}

	return ctx
}

func (handler *connStatsHandler) HandleRPC(ctx context.Context, stat stats.RPCStats) {
	if handler.next != nil {
		handler.next.HandleRPC(ctx, stat)
	}
}

func (handler *connStatsHandler) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	// 计数用的远端地址必须先挂上去, 再交给下一个 handler; 反过来会丢掉自己的 tag.
	tagged := context.WithValue(ctx, connTagKey{}, info)
	if handler.next != nil {
		return handler.next.TagConn(tagged, info)
	}

	return tagged
}

func (handler *connStatsHandler) HandleConn(ctx context.Context, stat stats.ConnStats) {
	switch stat.(type) {
	case *stats.ConnBegin:
		totalNum := atomic.AddInt64(&handler.server.ConnNum, 1)
		commonutil.GetLogger().Infof(ctx, "[server] 连接数=%d, 新连接: %s", totalNum, remoteAddr(ctx))
	case *stats.ConnEnd:
		totalNum := atomic.AddInt64(&handler.server.ConnNum, -1)
		commonutil.GetLogger().Debugf(ctx, "[server] 连接数=%d, 关闭连接: %s", totalNum, remoteAddr(ctx))
	}

	if handler.next != nil {
		handler.next.HandleConn(ctx, stat)
	}
}

func remoteAddr(ctx context.Context) string {
	info, ok := ctx.Value(connTagKey{}).(*stats.ConnTagInfo)
	if !ok || info == nil || info.RemoteAddr == nil {
		return "unknown"
	}

	return info.RemoteAddr.String()
}

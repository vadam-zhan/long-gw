package upstream

import (
	"context"

	gateway "github.com/vadam-zhan/long-gw/api/proto/v1"
	"github.com/vadam-zhan/long-gw/internal/types"
)

// Sender 上行消息发送接口
type Sender interface {
	// Send 发送上行消息
	Send(ctx context.Context, req *types.UpstreamRequest) error

	// Kind 返回发送器类型
	Kind() types.UpstreamKind

	Close()
}

// DownstreamRouter 下行路由接口
type DownstreamRouterInterface interface {
	RouteDownstreamMessage(msg *gateway.DownstreamKafkaMessage) error
}

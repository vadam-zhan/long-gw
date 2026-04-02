package downstream

import (
	gateway "github.com/vadam-zhan/long-gw/api/proto/v1"
)

// DownstreamRouter 下行路由接口
type DownstreamRouterInterface interface {
	RouteDownstreamMessage(msg *gateway.DownstreamKafkaMessage) error
}

package upstream

import (
	"context"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
)

// UpstreamSender
type UpstreamSender interface {
	Send(ctx context.Context, msg *gateway.Message) error
}

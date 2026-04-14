package connection

import (
	"context"

	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// HeartbeatHandler 心跳处理器
type HeartbeatHandler struct{}

func (h *HeartbeatHandler) Handle(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error {
	return nil // 心跳不需要特殊处理
}
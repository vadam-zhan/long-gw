package connection

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"go.uber.org/zap"
)

// HeartbeatHandler 处理心跳
type HeartbeatHandler struct{}

func (h *HeartbeatHandler) Handle(conn ConnectionAccessor, msg *types.Message) error {
	msg.Type = gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PONG
	select {
	case conn.GetWriteCh() <- msg:
	default:
		logger.Warn("write channel full, closing slow client",
			zap.String("remote", conn.GetRemoteAddr()))
	}

	return nil
}

package connection

import (
	gateway "github.com/vadam-zhan/long-gw/api/proto/v1"
	"github.com/vadam-zhan/long-gw/internal/logger"
	"github.com/vadam-zhan/long-gw/internal/types"
	"go.uber.org/zap"
)

// AuthHandler 处理鉴权请求
type AuthHandler struct{}

func (h *AuthHandler) Handle(conn ConnectionAccessor, msg *types.Message) error {
	userID := msg.UserID
	deviceID := msg.DeviceID

	conn.SetUserInfo(userID, deviceID)

	// 这里需要调用 auth 的接口，通过 grpc 进行调用，验证请求的 token 是否是 auth 签发的
	// TODO @zhijing.zhang
	conn.SetAuthed(true)

	conn.RouterRegister(userID, deviceID)

	logger.Info("connection auth success",
		zap.String("user_id", userID),
		zap.String("device_id", deviceID))

	msg.Type = gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE
	select {
	case conn.GetWriteCh() <- msg:
	default:
		logger.Warn("write channel full, closing slow client",
			zap.String("remote", conn.GetRemoteAddr()))
	}

	return nil
}

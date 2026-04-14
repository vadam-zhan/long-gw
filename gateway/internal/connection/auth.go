package connection

import (
	"context"

	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// AuthHandler 鉴权处理器
type AuthHandler struct{}

func (h *AuthHandler) Handle(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error {
	// 解析 AuthPayload
	payload, ok := msg.Payload.(*types.AuthPayload)
	if !ok {
		return nil
	}

	// 设置用户信息
	conn.SetUserInfo(payload.Token, payload.Platform)
	conn.SetAuthed(true)

	// 注册路由
	userID, deviceID := conn.GetUserInfo()
	conn.RouterRegister(userID, deviceID)

	return nil
}
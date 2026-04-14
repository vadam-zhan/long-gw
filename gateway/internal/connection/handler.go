package connection

import (
	"context"
	"fmt"

	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// ConnectionAccessor 连接访问接口
type ConnectionAccessor interface {
	GetConnID() string
	SetUserInfo(userID, deviceID string)
	GetUserInfo() (userID, deviceID string)
	GetRemoteAddr() string
	GetWriteCh() chan *types.Message
	IsAuthed() bool
	SetAuthed(authed bool)
	RouterRegister(userID, deviceID string)
	RefreshRoute()
	SubmitUpstream(ctx context.Context, msg *types.Message) SubmitResult
}

// MsgHandler 消息处理接口
type MsgHandler interface {
	Handle(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error
}

// HandlerRegistry 消息处理注册表
type HandlerRegistry struct {
	handlers map[types.SignalType]MsgHandler
}

// NewHandlerRegistry 创建注册表
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: map[types.SignalType]MsgHandler{
			types.SignalTypeHeartbeatPing: &HeartbeatHandler{},
			types.SignalTypeBusinessUp:     &UpstreamHandler{},
			types.SignalTypeMessageAck:     &AckHandler{},
			types.SignalTypeAuthRequest:    &AuthHandler{},
		},
	}
}

// Register 注册处理器
func (r *HandlerRegistry) Register(msgType types.SignalType, handler MsgHandler) {
	r.handlers[msgType] = handler
}

// HandleMessage 处理消息
func (r *HandlerRegistry) HandleMessage(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error {
	handler, ok := r.handlers[msg.Type]
	if !ok {
		return fmt.Errorf("no handler for msg type: %v", msg.Type)
	}
	return handler.Handle(ctx, conn, msg)
}
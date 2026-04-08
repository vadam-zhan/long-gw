package connection

import (
	"fmt"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// GlobalHandlerRegistry 全局处理器注册表
var GlobalHandlerRegistry = NewHandlerRegistry()

// UpstreamSubmitter 提交上行消息到 worker 池的接口
type UpstreamSubmitter interface {
	SubmitUpstream(msg *types.Message) bool
}

// ConnectionAccessor 连接访问接口，供 handler 使用
// 定义在 logic/connection 包中，避免与 connector 包循环依赖
type ConnectionAccessor interface {
	GetConnID() string
	SetUserInfo(userID, deviceID string)
	GetUserInfo() (userID, deviceID string)
	GetRemoteAddr() string
	GetWriteCh() chan *types.Message
	RouterRegister(userID, deviceID string)
	IsAuthed() bool
	SetAuthed(authed bool)
	RefreshRoute()
	GetUpstreamSubmitter() UpstreamSubmitter
}

// MsgHandler 消息处理接口
type MsgHandler interface {
	Handle(conn ConnectionAccessor, msg *types.Message) error
}

// HandlerRegistry 消息处理注册表
type HandlerRegistry struct {
	handlers map[gateway.SignalType]MsgHandler
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: map[gateway.SignalType]MsgHandler{
			gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PING: &HeartbeatHandler{},
			gateway.SignalType_SIGNAL_TYPE_AUTH_REQUEST:   &AuthHandler{},
			gateway.SignalType_SIGNAL_TYPE_BUSINESS_UP:    &UpstreamHandler{},
			// 其他消息类型默认走 UpstreamHandler
		},
	}
}

func (r *HandlerRegistry) Get(msgType gateway.SignalType) MsgHandler {
	if h, ok := r.handlers[msgType]; ok {
		return h
	}
	return nil
}

// HandleMessage 根据消息类型分发处理
func (r *HandlerRegistry) HandleMessage(conn ConnectionAccessor, msg *types.Message) error {
	handler := r.Get(msg.Type)
	if handler == nil {
		return fmt.Errorf("unknown message type: %d", msg.Type)
	}
	return handler.Handle(conn, msg)
}

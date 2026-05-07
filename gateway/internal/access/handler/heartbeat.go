package handler

import (
	gatewayv1 "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

type HeartbeatHandler struct{}

func (h *HeartbeatHandler) Handle(sess types.HandlerSession, conn types.ConnSubmitter, msg *gatewayv1.Message) error {
	if msg.Type != gatewayv1.FrameType_PING {
		// 写入错误信息
		conn.Submit(&gatewayv1.Message{
			Type: gatewayv1.FrameType_ERROR,
			Body: &gatewayv1.Body{
				Data: []byte("invalid message type"),
			},
		})
		return nil
	}

	conn.Submit(&gatewayv1.Message{
		Type: gatewayv1.FrameType_PONG,
		Body: &gatewayv1.Body{
			Data: []byte("pong"),
		},
	})
	return nil
}

package handler

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

type HeartbeatHandler struct{}

func (h *HeartbeatHandler) Handle(sess types.HandlerSession, conn types.ConnSubmitter, msg *gateway.Message) error {
	if msg.Type != gateway.SignalType_PING {
		// 写入错误信息
		conn.Submit(&gateway.Message{
			Type: gateway.SignalType_ERROR,
			Body: &gateway.Body{
				Payload: []byte("invalid message type"),
			},
		})
		return nil
	}

	conn.Submit(&gateway.Message{
		Type: gateway.SignalType_PONG,
		Body: &gateway.Body{
			Payload: []byte("pong"),
		},
	})
	return nil
}

package handler

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/contracts"
)

type HeartbeatHandler struct{}

func (h *HeartbeatHandler) Handle(sess contracts.SessionAccessor, conn *connection.Connection, msg *gateway.Message) error {
	if msg.Type != gateway.MsgType_MSG_TYPE_PING {
		// 写入错误信息
		conn.Send(&gateway.Message{
			Type: gateway.MsgType_MSG_TYPE_ERROR,
			Body: &gateway.Body{
				Payload: []byte("invalid message type"),
			},
		})
		return nil
	}

	conn.Send(&gateway.Message{
		Type: gateway.MsgType_MSG_TYPE_PONG,
		Body: &gateway.Body{
			Payload: []byte("pong"),
		},
	})
	return nil
}

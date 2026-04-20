package handler

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// AckHandler ACK 处理器
type AckHandler struct{}

func (h *AckHandler) Handle(sess types.HandlerSession, conn types.ConnSubmitter, msg *gateway.Message) error {

	sess.Ack(msg.MsgId)

	return nil
}

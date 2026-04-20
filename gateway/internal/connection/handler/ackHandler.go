package handler

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/contracts"
)

// AckHandler ACK 处理器
type AckHandler struct{}

func (h *AckHandler) Handle(sess contracts.SessionAccessor, conn *connection.Connection, msg *gateway.Message) error {
	// payload, ok := msg.Payload.(*types.AckPayload)
	// if !ok {
	// 	return nil
	// }

	// // 逐条处理 ACK
	// for _, ack := range payload.Acks {
	// 	conn.SubmitUpstream(ctx, &types.Message{
	// 		Type: types.SignalTypeMessageAck,
	// 		Payload: &types.AckPayload{
	// 			Acks: []types.AckItem{{MsgID: ack.MsgID, Success: ack.Success}},
	// 		},
	// 	})
	// }

	return nil
}

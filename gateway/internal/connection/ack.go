package connection

import (
	"context"

	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// AckHandler ACK 处理器
type AckHandler struct{}

func (h *AckHandler) Handle(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error {
	payload, ok := msg.Payload.(*types.AckPayload)
	if !ok {
		return nil
	}

	// 逐条处理 ACK
	for _, ack := range payload.Acks {
		conn.SubmitUpstream(ctx, &types.Message{
			Type: types.SignalTypeMessageAck,
			Payload: &types.AckPayload{
				Acks: []types.AckItem{{MsgID: ack.MsgID, Success: ack.Success}},
			},
		})
	}

	return nil
}
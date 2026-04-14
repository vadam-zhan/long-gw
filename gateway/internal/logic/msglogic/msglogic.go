package msglogic

import (
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// SystemTimestamp 返回当前时间戳（毫秒）
func SystemTimestamp() int64 {
	return time.Now().UnixMilli()
}

// ===============================
// Decode: ClientSignal → Message
// ===============================

// Decode 解析 ClientSignal 数据为内部 Message
// 输入：proto ClientSignal（Wire format）
// 输出：types.Message（内部业务表示）
func Decode(cs *gateway.ClientSignal) (*types.Message, error) {
	msg := &types.Message{
		RequestID:  cs.RequestId,
		SequenceID: cs.SequenceId,
		Type:       types.SignalTypeFromProto(cs.SignalType),
	}

	switch payload := cs.Payload.(type) {
	case *gateway.ClientSignal_HeartbeatPing:
		msg.Payload = &types.HeartbeatPayload{
			ClientTime: payload.HeartbeatPing.ClientTimestamp,
		}
		msg.ClientTimestamp = int64(payload.HeartbeatPing.ClientTimestamp)

	case *gateway.ClientSignal_AuthRequest:
		msg.UserID = payload.AuthRequest.UserId
		msg.DeviceID = payload.AuthRequest.DeviceId
		clientVer := ""
		if payload.AuthRequest.ClientVersion != nil {
			clientVer = *payload.AuthRequest.ClientVersion
		}
		heartbeatInterval := uint32(0)
		if payload.AuthRequest.HeartbeatInterval != nil {
			heartbeatInterval = *payload.AuthRequest.HeartbeatInterval
		}
		msg.Payload = &types.AuthPayload{
			Token:              payload.AuthRequest.Token,
			Platform:           payload.AuthRequest.Platform,
			ClientVer:          clientVer,
			HeartbeatInterval:  heartbeatInterval,
		}

	case *gateway.ClientSignal_LogoutRequest:
		msg.Payload = &types.LogoutPayload{
			Reason: payload.LogoutRequest.Reason,
		}

	case *gateway.ClientSignal_SubscribeRequest:
		msg.Payload = &types.SubscribePayload{
			Topic: payload.SubscribeRequest.Topic,
			Qos:   payload.SubscribeRequest.Qos,
		}

	case *gateway.ClientSignal_UnsubscribeRequest:
		msg.Payload = &types.SubscribePayload{
			Topic: payload.UnsubscribeRequest.Topic,
		}

	case *gateway.ClientSignal_MessageAck:
		// Ack 消息暂不处理具体内容，只做路由
		msg.Payload = &types.AckPayload{}

	case *gateway.ClientSignal_BusinessUp:
		msg.Payload = &types.BusinessPayload{
			MsgID:   payload.BusinessUp.MsgId,
			BizType: types.BusinessTypeFromProto(payload.BusinessUp.Type),
			RoomID:  "",
			Body:    payload.BusinessUp.Body,
		}

	}

	return msg, nil
}

// ===============================
// Encode: Message → ClientSignal
// ===============================

// Encode 将内部 Message 编码为 ClientSignal
// 输入：types.Message（内部业务表示）
// 输出：proto ClientSignal（Wire format，用于发送到客户端或后端）
func Encode(msg *types.Message) (*gateway.ClientSignal, error) {
	cs := &gateway.ClientSignal{
		RequestId:  msg.RequestID,
		SignalType: msg.Type.ToProto(),
	}

	switch msg.Type {
	case types.SignalTypeHeartbeatPong:
		// 从 Payload 提取 ClientTimestamp
		var clientTs uint64
		if hp, ok := msg.Payload.(*types.HeartbeatPayload); ok {
			clientTs = hp.ClientTime
		}
		cs.Payload = &gateway.ClientSignal_HeartbeatPong{
			HeartbeatPong: &gateway.HeartbeatPong{
				ServerTimestamp: uint64(SystemTimestamp()),
				ClientTimestamp: clientTs,
			},
		}

	case types.SignalTypeAuthResponse:
		cs.Payload = &gateway.ClientSignal_AuthResponse{
			AuthResponse: &gateway.AuthResponse{
				Code:               0,
				Msg:                "ok",
				Result:             true,
				HeartbeatInternal: 20,
			},
		}

	case types.SignalTypeLogoutResponse:
		cs.Payload = &gateway.ClientSignal_LogoutResponse{
			LogoutResponse: &gateway.LogoutResponse{
				Code: 0,
				Msg:  "ok",
			},
		}

	case types.SignalTypeSubscribeResponse:
		cs.Payload = &gateway.ClientSignal_SubscribeResponse{
			SubscribeResponse: &gateway.SubscribeResponse{
				Code: 0,
				Msg:  "ok",
			},
		}

	case types.SignalTypeUnsubscribeResponse:
		cs.Payload = &gateway.ClientSignal_UnsubscribeResponse{
			UnsubscribeResponse: &gateway.UnsubscribeResponse{
				Code: 0,
				Msg:  "ok",
			},
		}

	case types.SignalTypeBusinessDown:
		// 从 DownstreamPayload 提取业务数据
		var msgID string
		var bizType gateway.BusinessType
		var body []byte
		if dp, ok := msg.Payload.(*types.DownstreamPayload); ok {
			msgID = dp.MsgID
			bizType = dp.BizType.Proto()
			body = dp.Body
		}
		cs.Payload = &gateway.ClientSignal_BusinessDown{
			BusinessDown: &gateway.BusinessDown{
				MsgId: msgID,
				Type:  bizType,
				Body:  body,
			},
		}

	default:
		// 其他消息类型（控制类）不支持编码为 ClientSignal
		return nil, nil
	}

	return cs, nil
}
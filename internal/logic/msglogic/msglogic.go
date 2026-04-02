package msglogic

import (
	"time"

	gateway "github.com/vadam-zhan/long-gw/api/proto/v1"
	"github.com/vadam-zhan/long-gw/internal/types"
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
		Type:       cs.SignalType,
	}

	switch payload := cs.Payload.(type) {
	case *gateway.ClientSignal_HeartbeatPing:
		msg.ClientTimestamp = payload.HeartbeatPing.ClientTimestamp

	case *gateway.ClientSignal_AuthRequest:
		msg.UserID = payload.AuthRequest.UserId
		msg.DeviceID = payload.AuthRequest.DeviceId
		msg.AuthToken = payload.AuthRequest.Token
		msg.Platform = payload.AuthRequest.Platform
		// Note: ClientVersion 和 HeartbeatInterval 可按需扩展

	case *gateway.ClientSignal_LogoutRequest:
		msg.Reason = payload.LogoutRequest.Reason

	case *gateway.ClientSignal_SubscribeRequest:
		msg.Topic = payload.SubscribeRequest.Topic
		msg.Qos = payload.SubscribeRequest.Qos

	case *gateway.ClientSignal_UnsubscribeRequest:
		msg.Topic = payload.UnsubscribeRequest.Topic

	case *gateway.ClientSignal_MessageAck:
		// Ack 消息暂不处理具体内容，只做路由

	case *gateway.ClientSignal_BusinessUp:
		msg.MsgID = payload.BusinessUp.MsgId
		msg.BizType = payload.BusinessUp.Type
		msg.Body = payload.BusinessUp.Body

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
		SignalType: msg.Type,
	}

	switch msg.Type {
	case gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PONG:
		cs.Payload = &gateway.ClientSignal_HeartbeatPong{
			HeartbeatPong: &gateway.HeartbeatPong{
				ServerTimestamp: uint64(SystemTimestamp()),
				ClientTimestamp: msg.ClientTimestamp,
			},
		}

	case gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE:
		cs.Payload = &gateway.ClientSignal_AuthResponse{
			AuthResponse: &gateway.AuthResponse{
				Code:              0,
				Msg:               "ok",
				Result:            true,
				HeartbeatInternal: 30,
			},
		}

	case gateway.SignalType_SIGNAL_TYPE_LOGOUT_RESPONSE:
		cs.Payload = &gateway.ClientSignal_LogoutResponse{
			LogoutResponse: &gateway.LogoutResponse{
				Code: 0,
				Msg:  "ok",
			},
		}

	case gateway.SignalType_SIGNAL_TYPE_SUBSCRIBE_RESPONSE:
		cs.Payload = &gateway.ClientSignal_SubscribeResponse{
			SubscribeResponse: &gateway.SubscribeResponse{
				Code: 0,
				Msg:  "ok",
			},
		}

	case gateway.SignalType_SIGNAL_TYPE_UNSUBSCRIBE_RESPONSE:
		cs.Payload = &gateway.ClientSignal_UnsubscribeResponse{
			UnsubscribeResponse: &gateway.UnsubscribeResponse{
				Code: 0,
				Msg:  "ok",
			},
		}

	case gateway.SignalType_SIGNAL_TYPE_BUSINESS_DOWN:
		cs.Payload = &gateway.ClientSignal_BusinessDown{
			BusinessDown: &gateway.BusinessDown{
				MsgId: msg.MsgID,
				Type:  msg.BizType,
				Body:  msg.Body,
			},
		}

	default:
		// 其他消息类型（控制类）不支持编码为 ClientSignal
		return nil, nil
	}

	return cs, nil
}

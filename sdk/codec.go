package sdk

import (
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// EncodeHeartbeatPing 编码心跳请求
func EncodeHeartbeatPing(requestID string, clientTimestamp uint64) *gateway.ClientSignal {
	return &gateway.ClientSignal{
		RequestId:  requestID,
		SignalType: gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PING,
		Payload: &gateway.ClientSignal_HeartbeatPing{
			HeartbeatPing: &gateway.HeartbeatPing{
				ClientTimestamp: clientTimestamp,
			},
		},
	}
}

// EncodeAuthRequest 编码鉴权请求
func EncodeAuthRequest(requestID string, auth *AuthRequest) *gateway.ClientSignal {
	req := &gateway.AuthRequest{
		Token:    auth.Token,
		UserId:   auth.UserID,
		DeviceId: auth.DeviceID,
		Platform: auth.Platform,
	}
	if auth.HeartbeatInterval > 0 {
		req.HeartbeatInterval = &auth.HeartbeatInterval
	}
	return &gateway.ClientSignal{
		RequestId:  requestID,
		SignalType: gateway.SignalType_SIGNAL_TYPE_AUTH_REQUEST,
		Payload: &gateway.ClientSignal_AuthRequest{
			AuthRequest: req,
		},
	}
}

// EncodeSubscribeRequest 编码订阅请求
func EncodeSubscribeRequest(requestID string, sub *SubscribeRequest) *gateway.ClientSignal {
	return &gateway.ClientSignal{
		RequestId:  requestID,
		SignalType: gateway.SignalType_SIGNAL_TYPE_SUBSCRIBE_REQUEST,
		Payload: &gateway.ClientSignal_SubscribeRequest{
			SubscribeRequest: &gateway.SubscribeRequest{
				Topic: sub.Topic,
				Qos:   sub.Qos,
			},
		},
	}
}

// EncodeUnsubscribeRequest 编码取消订阅请求
func EncodeUnsubscribeRequest(requestID string, topic string) *gateway.ClientSignal {
	return &gateway.ClientSignal{
		RequestId:  requestID,
		SignalType: gateway.SignalType_SIGNAL_TYPE_UNSUBSCRIBE_REQUEST,
		Payload: &gateway.ClientSignal_UnsubscribeRequest{
			UnsubscribeRequest: &gateway.UnsubscribeRequest{
				Topic: topic,
			},
		},
	}
}

// EncodeBusinessUp 编码业务上行消息
func EncodeBusinessUp(requestID string, seq uint64, biz *BusinessMessage) *gateway.ClientSignal {
	return &gateway.ClientSignal{
		RequestId:  requestID,
		SequenceId: seq,
		SignalType: gateway.SignalType_SIGNAL_TYPE_BUSINESS_UP,
		Payload: &gateway.ClientSignal_BusinessUp{
			BusinessUp: &gateway.BusinessUp{
				MsgId: biz.MsgID,
				Type:  biz.BizType.ToProto(),
				Body:  biz.Body,
			},
		},
	}
}

// EncodeLogoutRequest 编码登出请求
func EncodeLogoutRequest(requestID string, reason uint32) *gateway.ClientSignal {
	return &gateway.ClientSignal{
		RequestId:  requestID,
		SignalType: gateway.SignalType_SIGNAL_TYPE_LOGOUT_REQUEST,
		Payload: &gateway.ClientSignal_LogoutRequest{
			LogoutRequest: &gateway.LogoutRequest{
				Reason: reason,
			},
		},
	}
}

// EncodeMessageAck 编码消息确认
func EncodeMessageAck(requestID string, msgIDs []string) *gateway.ClientSignal {
	return &gateway.ClientSignal{
		RequestId:  requestID,
		SignalType: gateway.SignalType_SIGNAL_TYPE_MESSAGE_ACK,
		Payload: &gateway.ClientSignal_MessageAck{
			MessageAck: &gateway.MessageAck{
				AckMsgId: msgIDs,
			},
		},
	}
}

// DecodeClientSignal 解码 ClientSignal → Message
func DecodeClientSignal(cs *gateway.ClientSignal) *Message {
	return &Message{
		RequestID:  cs.RequestId,
		SequenceID: cs.SequenceId,
		Type:       SignalTypeFromProto(cs.SignalType),
	}
}

// DecodeAuthResponse 解码鉴权响应
func DecodeAuthResponse(cs *gateway.ClientSignal) *AuthResponse {
	if cs.Payload == nil {
		return nil
	}
	authResp, ok := cs.Payload.(*gateway.ClientSignal_AuthResponse)
	if !ok {
		return nil
	}
	return &AuthResponse{
		Code:              authResp.AuthResponse.Code,
		Msg:               authResp.AuthResponse.Msg,
		Result:            authResp.AuthResponse.Result,
		HeartbeatInterval: authResp.AuthResponse.HeartbeatInternal,
	}
}

// DecodeBusinessDown 解码业务下行消息
func DecodeBusinessDown(cs *gateway.ClientSignal) *Message {
	if cs.Payload == nil {
		return nil
	}
	bizDown, ok := cs.Payload.(*gateway.ClientSignal_BusinessDown)
	if !ok {
		return nil
	}
	return &Message{
		RequestID:  cs.RequestId,
		SequenceID: cs.SequenceId,
		Type:       SignalTypeBusinessDown,
		MsgID:      bizDown.BusinessDown.MsgId,
		BizType:    BusinessTypeFromProto(bizDown.BusinessDown.Type),
		Body:       bizDown.BusinessDown.Body,
	}
}

// CurrentTimestamp 返回当前时间戳(毫秒)
func CurrentTimestamp() uint64 {
	return uint64(time.Now().UnixMilli())
}

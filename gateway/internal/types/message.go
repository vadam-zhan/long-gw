package types

import (
    gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// Message 是内部统一消息表示
type Message struct {
    // ===== 传输层元信息 =====
    RequestID  string
    SequenceID uint64
    Type       SignalType

    // ===== 发送方上下文 =====
    ConnID   string
    UserID   string
    DeviceID string

    // ===== 消息载荷 =====
    Payload MessagePayload

    // ===== 可选元信息 =====
    Metadata map[string]string
    Tags     []string

    // ===== 时间戳 =====
    ClientTimestamp  int64
    ServerTimestamp int64
}

// SignalType 信令类型
type SignalType int32

const (
    SignalTypeHeartbeatPing       SignalType = 1
    SignalTypeHeartbeatPong       SignalType = 2
    SignalTypeAuthRequest         SignalType = 3
    SignalTypeAuthResponse        SignalType = 4
    SignalTypeMessageAck          SignalType = 5
    SignalTypeLogoutRequest       SignalType = 7
    SignalTypeLogoutResponse      SignalType = 8
    SignalTypeSubscribeRequest    SignalType = 9
    SignalTypeSubscribeResponse   SignalType = 10
    SignalTypeUnsubscribeRequest  SignalType = 11
    SignalTypeUnsubscribeResponse SignalType = 12
    SignalTypeBusinessUp          SignalType = 100
    SignalTypeBusinessDown        SignalType = 101
)

// ToProto 转换为 proto SignalType
func (s SignalType) ToProto() gateway.SignalType {
    return gateway.SignalType(s)
}

// SignalTypeFromProto 从 proto 转换
func SignalTypeFromProto(p gateway.SignalType) SignalType {
    return SignalType(p)
}
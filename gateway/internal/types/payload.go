package types

// ==============================
// Payload 接口
// ==============================

// MessagePayload 消息载荷接口
type MessagePayload interface {
    isMessagePayload()
}

// ==============================
// 控制消息载荷
// ==============================

// AuthPayload 鉴权载荷
type AuthPayload struct {
    Token       string
    Platform    string
    ClientVer   string
    HeartbeatInterval uint32
}

func (*AuthPayload) isMessagePayload() {}

// HeartbeatPayload 心跳载荷
type HeartbeatPayload struct {
    ClientTime uint64
}

func (*HeartbeatPayload) isMessagePayload() {}

// LogoutPayload 登出载荷
type LogoutPayload struct {
    Reason uint32
}

func (*LogoutPayload) isMessagePayload() {}

// SubscribePayload 订阅载荷
type SubscribePayload struct {
    Topic string
    Qos   uint32
}

func (*SubscribePayload) isMessagePayload() {}

// ==============================
// 业务消息载荷
// ==============================

// BusinessPayload 业务透传载荷
type BusinessPayload struct {
    MsgID   string
    BizType BusinessType
    RoomID  string
    Body    []byte
}

func (*BusinessPayload) isMessagePayload() {}

// AckPayload 消息确认载荷
type AckPayload struct {
    Acks []AckItem
}

type AckItem struct {
    MsgID   string
    Success bool
}

func (*AckPayload) isMessagePayload() {}

// ==============================
// 下行消息载荷
// ==============================

// DownstreamPayload 下行消息载荷
type DownstreamPayload struct {
    MsgID      string
    BizType    BusinessType
    RoomID     string
    RoomType   RoomType
    FromUserID string
    Body       []byte
    Qos        QosLevel
    NeedAck    bool
}

func (*DownstreamPayload) isMessagePayload() {}

// RoomType 房间类型
type RoomType int8

const (
    RoomTypeSingle   RoomType = 1
    RoomTypeGroup    RoomType = 2
    RoomTypeLiveRoom RoomType = 3
)

// QosLevel QoS 级别
type QosLevel int8

const (
    QosAtMostOnce  QosLevel = 0
    QosAtLeastOnce QosLevel = 1
)
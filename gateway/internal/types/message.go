package types

import gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"

// Message 解析后的内部消息表示
// 设计原则：
// 1. 字段语义业务化，与 proto 解耦
// 2. 根据 Type 不同，特定字段才有效（见各字段注释）
// 3. 控制消息不需要路由到后端，业务消息才需要
type Message struct {
	// ===== 通用字段 =====
	RequestID  string             // 请求ID，用于全链路追踪（对应 ClientSignal.request_id）
	SequenceID uint64             // 消息序列号，防重放（对应 ClientSignal.sequence_id）
	Type       gateway.SignalType // 消息类型

	// ===== 载荷 =====
	Body []byte // 原始载荷，业务消息使用

	// ===== 业务消息专用（Type=BusinessUp/BusinessDown 时有效） =====
	MsgID   string               // 业务消息唯一ID
	BizType gateway.BusinessType // 业务类型

	// ===== 控制消息专用字段 =====
	// AuthRequest 时有效
	UserID    string // 用户ID
	DeviceID  string // 设备ID
	AuthToken string // 鉴权 Token
	Platform  string // 客户端平台

	// HeartbeatPing 时有效
	ClientTimestamp uint64 // 客户端发送时间戳

	// LogoutRequest 时有效
	Reason uint32 // 登出原因

	// SubscribeRequest/UnsubscribeRequest 时有效
	Topic string // 订阅主题
	Qos   uint32 // 订阅质量等级
}

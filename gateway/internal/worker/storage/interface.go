package storage

import (
	"context"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// OfflineMessage 离线消息结构
type OfflineMessage struct {
	MsgID      string
	UserID     string
	DeviceID   string
	FromUserID string
	MsgType    gateway.BusinessType
	RoomType   int8
	Payload    []byte
	SeqID      int64
	Timestamp  int64
}

// OfflineStore 离线消息存储接口
type OfflineStore interface {
	// Save 保存离线消息
	Save(ctx context.Context, msg *OfflineMessage) error

	// Get 获取用户离线消息（按 seq_id 升序）
	Get(ctx context.Context, userID string, limit int) ([]*OfflineMessage, error)

	// MarkDelivered 标记消息已送达
	MarkDelivered(ctx context.Context, msgID string) error

	// Delete 删除离线消息
	Delete(ctx context.Context, msgID string) error

	// Count 获取用户离线消息数量
	Count(ctx context.Context, userID string) (int64, error)

	Store(ctx context.Context, msg *gateway.Message) error
}

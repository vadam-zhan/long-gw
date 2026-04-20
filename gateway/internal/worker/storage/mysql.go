package storage

import (
	"context"
	"log/slog"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	proto "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/storage/model"

	"gorm.io/gorm"
)

// MySQLStore MySQL 离线消息存储实现
type MySQLStore struct {
	db *gorm.DB
}

// NewMySQLStore 创建 MySQL 存储实例
func NewMySQLStore(db *gorm.DB) *MySQLStore {
	// 自动迁移表结构
	db.AutoMigrate(&model.OfflineMessageModel{})
	return &MySQLStore{db: db}
}

// Save 保存离线消息
func (s *MySQLStore) Store(ctx context.Context, msg *gateway.Message) error {
	m := &model.OfflineMessageModel{}

	result := s.db.WithContext(ctx).Create(m)
	if result.Error != nil {
		slog.Error("save offline message failed",
			"msg_id", msg.MsgId,
			"error", result.Error)
		return result.Error
	}
	return nil
}

func (s *MySQLStore) Fetch(ctx context.Context, userID string, afterSeq uint64) ([]*proto.Message, error) {
	var models []*model.OfflineMessageModel

	result := s.db.WithContext(ctx).
		Where("user_id = ? AND seq_id > ?", userID, afterSeq).
		Order("seq_id ASC").
		Find(&models)

	if result.Error != nil {
		return nil, result.Error
	}

	messages := make([]*proto.Message, len(models))
	for i := range models {
		messages[i] = &proto.Message{}
	}
	return messages, nil
}

func (s *MySQLStore) Delete(ctx context.Context, msgID string) error {
	result := s.db.WithContext(ctx).Delete(&model.OfflineMessageModel{MsgID: msgID})
	if result.Error != nil {
		slog.Error("delete offline message failed",
			"msg_id", msgID,
			"error", result.Error)
		return result.Error
	}
	return nil
}

// MarkDelivered 标记消息已送达
func (s *MySQLStore) MarkDelivered(ctx context.Context, msgID string) error {
	result := s.db.WithContext(ctx).
		Model(&model.OfflineMessageModel{}).
		Where("msg_id = ?", msgID).
		Update("delivered_at", time.Now())
	return result.Error
}

// Count 获取用户离线消息数量
func (s *MySQLStore) Count(ctx context.Context, userID string) (int64, error) {
	var count int64
	result := s.db.WithContext(ctx).
		Model(&model.OfflineMessageModel{}).
		Where("user_id = ? AND delivered_at IS NULL", userID).
		Count(&count)
	return count, result.Error
}

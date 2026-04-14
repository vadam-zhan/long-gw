package storage

import (
    "context"
    "time"

    gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
    "gorm.io/gorm"

    "github.com/vadam-zhan/long-gw/gateway/internal/logger"
    "github.com/vadam-zhan/long-gw/gateway/internal/worker/storage/model"
    "go.uber.org/zap"
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
func (s *MySQLStore) Save(ctx context.Context, msg *OfflineMessage) error {
    m := &model.OfflineMessageModel{
        MsgID:      msg.MsgID,
        UserID:     msg.UserID,
        DeviceID:   msg.DeviceID,
        FromUserID: msg.FromUserID,
        MsgType:    int8(msg.MsgType),
        RoomType:   msg.RoomType,
        Payload:    msg.Payload,
        SeqID:      msg.SeqID,
    }

    result := s.db.WithContext(ctx).Create(m)
    if result.Error != nil {
        logger.Error("save offline message failed",
            zap.String("msg_id", msg.MsgID),
            zap.Error(result.Error))
        return result.Error
    }
    return nil
}

// Get 获取用户离线消息
func (s *MySQLStore) Get(ctx context.Context, userID string, limit int) ([]*OfflineMessage, error) {
    var models []*model.OfflineMessageModel

    result := s.db.WithContext(ctx).
        Where("user_id = ? AND delivered_at IS NULL", userID).
        Order("seq_id ASC").
        Limit(limit).
        Find(&models)

    if result.Error != nil {
        return nil, result.Error
    }

    messages := make([]*OfflineMessage, len(models))
    for i, m := range models {
        messages[i] = &OfflineMessage{
            MsgID:     m.MsgID,
            UserID:    m.UserID,
            DeviceID:  m.DeviceID,
            FromUserID: m.FromUserID,
            MsgType:   gateway.BusinessType(m.MsgType),
            RoomType:  m.RoomType,
            Payload:   m.Payload,
            SeqID:     m.SeqID,
        }
    }
    return messages, nil
}

// MarkDelivered 标记消息已送达
func (s *MySQLStore) MarkDelivered(ctx context.Context, msgID string) error {
    result := s.db.WithContext(ctx).
        Model(&model.OfflineMessageModel{}).
        Where("msg_id = ?", msgID).
        Update("delivered_at", time.Now())
    return result.Error
}

// Delete 删除离线消息
func (s *MySQLStore) Delete(ctx context.Context, msgID string) error {
    result := s.db.WithContext(ctx).
        Where("msg_id = ?", msgID).
        Delete(&model.OfflineMessageModel{})
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
package model

import (
    "time"
)

// OfflineMessageModel GORM 模型
type OfflineMessageModel struct {
    ID          int64      `gorm:"primaryKey;autoIncrement"`
    MsgID       string     `gorm:"type:varchar(64);not null;index:idx_msg_id"`
    UserID      string     `gorm:"type:varchar(64);not null;index:idx_user_unread;index:idx_user_seq"`
    DeviceID    string     `gorm:"type:varchar(64)"`
    FromUserID  string     `gorm:"type:varchar(64);not null"`
    MsgType     int8       `gorm:"type:tinyint;not null"`
    RoomType    int8       `gorm:"type:tinyint;not null"`
    Payload     []byte     `gorm:"type:mediumblob;not null"`
    SeqID       int64      `gorm:"not null;index:idx_user_seq"`
    CreatedAt   time.Time  `gorm:"autoCreateTime"`
    DeliveredAt *time.Time `gorm:"default:null;index:idx_user_unread"`
}

func (OfflineMessageModel) TableName() string {
    return "offline_messages"
}
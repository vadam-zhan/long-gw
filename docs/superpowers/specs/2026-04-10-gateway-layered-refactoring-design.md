# Gateway 分层重构设计方案

**日期**: 2026-04-10
**状态**: 设计完成，待实施
**目标**: 分离传输层、连接层、业务层，让 Session 只负责协调
**版本**: v4.0（支持消息送达保证 + 离线消息存储）

---

## 1. 背景与问题

### 1.1 当前问题

Session (`session/session.go`) 当前承担了 8+ 种职责，违反单一职责原则和依赖反转原则，各层耦合紧密，难以单独测试和扩展。

### 1.2 新增需求（来自 2026-04-10-im-gateway-design）

| 需求 | 说明 |
|------|------|
| **消息送达保证** | At-least-once（至少一次），需客户端 ACK + 服务端重试 |
| **离线消息存储** | IM/推送消息需持久化，用户上线时投递 |
| **消息分类处理** | IM（需存储）、Live（直接转发）、Push（需存储） |
| **多协议支持** | WebSocket + TCP + MQTT |

### 1.3 重构目标

| 层级 | 职责 | 边界 |
|------|------|------|
| **Transport** | 纯 I/O：封装 TCP/WebSocket/gRPC/MQTT | 不管协议解析、业务逻辑 |
| **Connection** | 协议编解码、双 Goroutine、Handler 调度 | 不管 Worker、消息队列 |
| **Worker** | UpstreamSender、WorkerPool、Kafka Consumer、离线存储 | 不管连接细节、传输协议 |
| **Session** | 依赖注入协调、HTTP 服务、连接计数、生命周期 | 不管具体业务处理 |

---

## 2. 核心矛盾与解决方案

### 2.1 循环依赖问题分析

```
┌─────────────┐         ┌─────────────┐
│ Connection  │────────>│   Worker    │
│  (connection层) │<────────│  (worker层)  │
└─────────────┘         └─────────────┘
        │                       │
        │   Connection 需要提交  │   Worker 需要写入
        │   上行消息给 Worker    │   下行消息给 Connection
        │                       │
        └───────────────────────┘
              形成循环依赖
```

**问题本质**：
1. Connection 需要持有 `UpstreamSubmitter` 接口 → connection 层依赖 worker 层
2. Worker 的 DownstreamRouter 需要持有 Connection 引用 → worker 层依赖 connection 层

### 2.2 解决方案：接口定义在消费方 + Session 协调

**核心原则**：
> **接口由消费方定义，实现方依赖接口**

```
┌────────────────────────────────────────────────────────────────────────────────┐
│                              Session (协调层)                                  │
│                                                                                │
│   Session 持有所有跨层交互的协调逻辑：                                           │
│   · 创建 WorkerManager                                                        │
│   · 创建 ConnectionFactory                                                   │
│   · 协调 Connection 与 Worker 的注册关系                                       │
│                                                                                │
│   ┌──────────────────────────────────────────────────────────────────────┐   │
│   │                     ConnectionRegistry                                 │   │
│   │   · Register(connID, ConnectionWriter)                                │   │
│   │   · Unregister(connID)                                                │   │
│   │   · Get(connID) (ConnectionWriter)                                    │   │
│   └──────────────────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────────────────────┘
                                    │
                    Session 协调 Connection 和 Worker 的交互
                                    │
          ┌─────────────────────────┴─────────────────────────┐
          ▼                                                   ▼
┌─────────────────────┐                         ┌─────────────────────┐
│    Connection       │                         │       Worker        │
│                     │                         │                     │
│  · 持有 UpstreamSender                        │  · 持有 ConnectionRegistry              │
│    (接口，由 Session                          │    (接口，由 Session                     │
│     注入)                                    │     注入)                                │
│                     │                         │                     │
│  · 调用 Submitter    │                         │  · 调用 Registry   │
│    提交上行          │                         │    获取 Writer     │
│                     │                         │    写入下行         │
└─────────────────────┘                         └─────────────────────┘
          │                                                   ▲
          │           无直接依赖                              │
          │                                                   │
          └───────────────────────────────────────────────────┘
                        Session 作为桥梁协调
```

**关键决策**：
1. **接口定义位置**：
   - `UpstreamSender` 接口定义在 `connection` 包（消费方），由 `worker` 包实现
   - `ConnectionRegistry` 接口定义在 `worker` 包（消费方），由 `session` 包实现

2. **Session 作为协调者**：
   - Session 同时持有 WorkerManager 和 ConnectionRegistry
   - Session 负责将 Connection 注册到 Worker 的 Registry
   - Connection 和 Worker 通过 Session 间接交互，无直接依赖

---

## 3. 消息送达保证设计

### 3.1 消息类型与策略

| 消息类型 | 送达保证 | 离线存储 | 投递重试 | 客户端 ACK |
|----------|---------|---------|---------|------------|
| **IM** | At-least-once | MySQL/MongoDB | 是 | 是 |
| **Live** | At-most-ononce | 否 | 否 | 否 |
| **Push** | At-least-once | MySQL/MongoDB | 是 | 是 |

### 3.2 At-least-once 实现机制

```
┌─────────┐                                              ┌─────────┐
│ Client  │                                              │ Business │
└────┬────┘                                              └────┬────┘
     │                                                        │
     │ 1. 发送消息 (msg_id, seq_id, timestamp)                │
     │───────────────────────────────────────────────────────>│
     │                                                        │
     │                           2. 业务处理                   │
     │                           3. 存储离线消息（IM/Push）   │
     │                                                        │
     │ 4. 下行消息 (msg_id, seq_id)                          │
     │<───────────────────────────────────────────────────────│
     │                                                        │
     │ 5. 客户端 ACK (msg_id, seq_id)                        │
     │───────────────────────────────────────────────────────>│
     │                                                        │
     │                           6. 标记已送达                 │
     │                           7. 删除离线消息               │
```

### 3.3 消息元数据结构

```json
{
  "msg_id": "uuid",
  "seq_id": 12345,
  "from": "user_id",
  "to": "user_id | room_id",
  "msg_type": "im | live | push",
  "room_type": "single | group | live_room",
  "timestamp": 1712739200,
  "payload": {...},
  "ack_required": true,
  "offline_storage": true
}
```

### 3.4 离线消息存储设计

#### 3.4.1 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                     Offline Message Store                    │
│                                                              │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐   │
│  │   MySQL     │    │  MongoDB     │    │   Redis     │   │
│  │ (主存储)     │    │ (消息内容)    │    │ (热点缓存)   │   │
│  └─────────────┘    └─────────────┘    └─────────────┘   │
│         │                  │                  │             │
│         └──────────────────┴──────────────────┘             │
│                            │                                 │
│                     消息存储层抽象                            │
└─────────────────────────────────────────────────────────────┘
```

#### 3.4.2 MySQL 表结构

```sql
CREATE TABLE offline_messages (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    msg_id VARCHAR(64) NOT NULL COMMENT '消息唯一ID',
    user_id VARCHAR(64) NOT NULL COMMENT '接收者用户ID',
    device_id VARCHAR(64) COMMENT '设备ID（可选）',
    msg_type TINYINT NOT NULL COMMENT '消息类型：1=IM, 2=Push',
    room_type TINYINT NOT NULL COMMENT '房间类型：1=single, 2=group, 3=live_room',
    from_user_id VARCHAR(64) NOT NULL COMMENT '发送者用户ID',
    payload MEDIUMBLOB NOT NULL COMMENT '消息内容',
    seq_id BIGINT NOT NULL COMMENT '消息序列号（用于排序）',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    delivered_at TIMESTAMP NULL COMMENT '送达时间（NULL=未送达）',
    INDEX idx_user_unread (user_id, delivered_at) COMMENT '查询用户未送达消息',
    INDEX idx_msg_id (msg_id) COMMENT '根据msg_id查询/去重',
    INDEX idx_user_seq (user_id, seq_id) COMMENT '根据seq_id排序'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='离线消息表';
```

#### 3.4.3 GORM Model 定义

```go
// ==============================
// storage/model/offline_message.go
// ==============================

package model

import (
    "time"
)

// OfflineMessage 离线消息模型
type OfflineMessage struct {
    ID          int64      `gorm:"primaryKey;autoIncrement" json:"id"`
    MsgID       string     `gorm:"type:varchar(64);not null;index:idx_msg_id" json:"msg_id"`
    UserID      string     `gorm:"type:varchar(64);not null;index:idx_user_unread;index:idx_user_seq" json:"user_id"`
    DeviceID    string     `gorm:"type:varchar(64)" json:"device_id"`
    MsgType     int8       `gorm:"type:tinyint;not null" json:"msg_type"`      // 1=IM, 2=Push
    RoomType    int8       `gorm:"type:tinyint;not null" json:"room_type"`     // 1=single, 2=group, 3=live_room
    FromUserID  string     `gorm:"type:varchar(64);not null" json:"from_user_id"`
    Payload     []byte     `gorm:"type:mediumblob;not null" json:"payload"`
    SeqID       int64      `gorm:"not null;index:idx_user_seq" json:"seq_id"`
    CreatedAt   time.Time  `gorm:"autoCreateTime" json:"created_at"`
    DeliveredAt *time.Time `gorm:"default:null;index:idx_user_unread" json:"delivered_at"`
}

func (OfflineMessage) TableName() string {
    return "offline_messages"
}
```

#### 3.4.4 配置设计

```go
// ==============================
// config/config.go
// ==============================

// Config 应用配置
type Config struct {
    Gateway  GatewayConfig  `json:"gateway" mapstructure:"gateway"`
    Redis    RedisConfig    `json:"redis" mapstructure:"redis"`
    Upstream InteractConfig `json:"upstream" mapstructure:"upstream"`
    Auth     AuthConfig     `json:"auth" mapstructure:"auth"`
    Log      LogConfig      `json:"log" mapstructure:"log"`
    Database DatabaseConfig `json:"database" mapstructure:"database"`  // 新增
}

// DatabaseConfig MySQL 数据库配置
type DatabaseConfig struct {
    DSN          string `json:"dsn" mapstructure:"dsn"`                   // 数据源名称
    MaxOpenConns int    `json:"max_open_conns" mapstructure:"max_open_conns"` // 最大打开连接数
    MaxIdleConns int    `json:"max_idle_conns" mapstructure:"max_idle_conns"` // 最大空闲连接数
    ConnMaxLife  int    `json:"conn_max_life" mapstructure:"conn_max_life"`   // 连接最大生命周期（秒）
}
```

**配置文件示例**：

```yaml
database:
  dsn: "root:password@tcp(127.0.0.1:3306)/gateway?charset=utf8mb4&parseTime=True&loc=Local"
  max_open_conns: 100
  max_idle_conns: 20
  conn_max_life: 3600
```

#### 3.4.5 ServiceContext 全局 DB

```go
// ==============================
// svc/svc.go
// ==============================

package svc

import (
    "context"
    "time"

    "github.com/redis/go-redis/v9"
    "gorm.io/driver/mysql"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"

    "github.com/vadam-zhan/long-gw/gateway/internal/config"
    "github.com/vadam-zhan/long-gw/gateway/internal/logger"
    "github.com/vadam-zhan/long-gw/gateway/internal/storage/model"
    "go.uber.org/zap"
)

type ServiceContext struct {
    context.Context
    Config      *config.Config
    RedisClient *redis.Client
    DB          *gorm.DB  // 全局 MySQL 数据库连接
}

func NewServiceContext(ctx context.Context, c *config.Config) *ServiceContext {
    svc := &ServiceContext{
        Context: ctx,
        Config:  c,
    }

    // 初始化 Redis（保持原有逻辑）
    svc.initRedis(c)

    // 初始化 MySQL 数据库
    svc.initDatabase(c)

    return svc
}

// initDatabase 初始化 MySQL 数据库连接
func (s *ServiceContext) initDatabase(c *config.Config) {
    if c.Database.DSN == "" {
        logger.Warn("mysql dsn not configured, offline storage disabled")
        return
    }

    // 配置 GORM
    db, err := gorm.Open(mysql.Open(c.Database.DSN), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })
    if err != nil {
        logger.Error("mysql connection failed",
            zap.Error(err),
            zap.String("dsn", c.Database.DSN))
        return
    }

    // 获取底层 sql.DB 并配置连接池
    sqlDB, err := db.DB()
    if err != nil {
        logger.Error("get sql.DB failed", zap.Error(err))
        return
    }

    // 配置连接池
    sqlDB.SetMaxOpenConns(c.Database.MaxOpenConns)
    sqlDB.SetMaxIdleConns(c.Database.MaxIdleConns)
    sqlDB.SetConnMaxLifetime(time.Duration(c.Database.ConnMaxLife) * time.Second)

    // 自动迁移表结构
    if err := db.AutoMigrate(&model.OfflineMessage{}); err != nil {
        logger.Error("mysql auto migrate failed", zap.Error(err))
        return
    }

    s.DB = db
    logger.Info("mysql connected",
        zap.String("dsn", c.Database.DSN),
        zap.Int("max_open_conns", c.Database.MaxOpenConns),
        zap.Int("max_idle_conns", c.Database.MaxIdleConns))
}

func (s *ServiceContext) Close() {
    if s.RedisClient != nil {
        s.RedisClient.Close()
    }
    if s.DB != nil {
        sqlDB, _ := s.DB.DB()
        sqlDB.Close()
    }
}
```

#### 3.4.6 OfflineStore 接口与 MySQL 实现

```go
// ==============================
// worker/storage/interface.go
// ==============================

package storage

import (
    "context"

    gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

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
}

// OfflineMessage 离线消息结构
type OfflineMessage struct {
    MsgID     string
    UserID    string
    DeviceID  string
    FromUserID string
    MsgType   gateway.BusinessType
    RoomType  int8
    Payload   []byte
    SeqID     int64
    Timestamp int64
}
```

```go
// ==============================
// worker/storage/mysql.go
// ==============================

package storage

import (
    "context"
    "time"

    "gorm.io/gorm"

    "github.com/vadam-zhan/long-gw/gateway/internal/logger"
    "go.uber.org/zap"
)

// MySQLStore MySQL 离线消息存储实现
type MySQLStore struct {
    db *gorm.DB
}

// NewMySQLStore 创建 MySQL 存储实例
func NewMySQLStore(db *gorm.DB) *MySQLStore {
    return &MySQLStore{db: db}
}

// Save 保存离线消息
func (s *MySQLStore) Save(ctx context.Context, msg *OfflineMessage) error {
    model := &OfflineMessageModel{
        MsgID:      msg.MsgID,
        UserID:     msg.UserID,
        DeviceID:   msg.DeviceID,
        FromUserID: msg.FromUserID,
        MsgType:    int8(msg.MsgType),
        RoomType:   msg.RoomType,
        Payload:    msg.Payload,
        SeqID:      msg.SeqID,
    }

    result := s.db.WithContext(ctx).Create(model)
    if result.Error != nil {
        logger.Error("save offline message failed",
            zap.String("msg_id", msg.MsgID),
            zap.String("user_id", msg.UserID),
            zap.Error(result.Error))
        return result.Error
    }

    logger.Debug("offline message saved",
        zap.String("msg_id", msg.MsgID),
        zap.String("user_id", msg.UserID))
    return nil
}

// Get 获取用户离线消息（按 seq_id 升序）
func (s *MySQLStore) Get(ctx context.Context, userID string, limit int) ([]*OfflineMessage, error) {
    var models []*OfflineMessageModel

    result := s.db.WithContext(ctx).
        Where("user_id = ? AND delivered_at IS NULL", userID).
        Order("seq_id ASC").
        Limit(limit).
        Find(&models)

    if result.Error != nil {
        logger.Error("get offline messages failed",
            zap.String("user_id", userID),
            zap.Error(result.Error))
        return nil, result.Error
    }

    // 转换为业务模型
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
        Model(&OfflineMessageModel{}).
        Where("msg_id = ?", msgID).
        Update("delivered_at", time.Now())

    if result.Error != nil {
        logger.Error("mark delivered failed",
            zap.String("msg_id", msgID),
            zap.Error(result.Error))
        return result.Error
    }

    return nil
}

// Delete 删除离线消息
func (s *MySQLStore) Delete(ctx context.Context, msgID string) error {
    result := s.db.WithContext(ctx).
        Where("msg_id = ?", msgID).
        Delete(&OfflineMessageModel{})

    if result.Error != nil {
        logger.Error("delete offline message failed",
            zap.String("msg_id", msgID),
            zap.Error(result.Error))
        return result.Error
    }

    return nil
}

// Count 获取用户离线消息数量
func (s *MySQLStore) Count(ctx context.Context, userID string) (int64, error) {
    var count int64
    result := s.db.WithContext(ctx).
        Model(&OfflineMessageModel{}).
        Where("user_id = ? AND delivered_at IS NULL", userID).
        Count(&count)

    if result.Error != nil {
        logger.Error("count offline messages failed",
            zap.String("user_id", userID),
            zap.Error(result.Error))
        return 0, result.Error
    }

    return count, nil
}

// OfflineMessageModel GORM 模型（数据库表结构）
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
```

### 3.5 ACK 处理流程

```
Client  ──►  Transport  ──►  Connection.ReadLoop
                                        │
                                        │ Decode
                                        ▼
                                  types.Message
                                        │
                                        │ 判断是否是 ACK 消息
                                        ▼
                              ┌─────────────────┐
                              │  msg.Type ==    │
                              │  SIGNAL_TYPE_   │
                              │  ACK            │
                              └────────┬────────┘
                                       │
                              ┌────────▼────────┐
                              │ AckHandler.Handle │
                              └────────┬────────┘
                                       │
                              ┌────────▼────────────────────────┐
                              │ Worker.DeliverResult(msg_id)   │
                              │  1. 标记消息已送达               │
                              │  2. 删除离线存储                 │
                              │  3. 更新 seq_id                 │
                              └─────────────────────────────────┘
```

---

## 4. Message 结构设计

### 4.1 设计原则

| 原则 | 说明 |
|------|------|
| **协议无关** | 与 proto 定义解耦，内部统一表示 |
| **业务无关** | 支持 IM、Live、Push 等多种业务 |
| **层次清晰** | 传输层元信息 → 发送方上下文 → 消息载荷 |
| **可扩展** | Metadata 支持业务自定义扩展 |

### 4.2 核心结构

```go
// ==============================
// message.go (types 包)
// ==============================

// Message 是内部统一消息表示
type Message struct {
    // ===== 传输层元信息 =====
    RequestID  string            // 全链路追踪 ID
    SequenceID uint64            // 连接内单调递增，防重放
    Type       SignalType        // 消息类型（控制/业务/ACK）

    // ===== 发送方上下文 =====
    ConnID   string              // 连接 ID
    UserID   string              // 用户 ID（鉴权后有效）
    DeviceID string              // 设备 ID（鉴权后有效）

    // ===== 消息载荷 =====
    Payload  MessagePayload       // 消息内容（接口，支持多态）

    // ===== 可选元信息 =====
    Metadata map[string]string   // 业务扩展字段
    Tags     []string            // 消息标签（用于路由、过滤）

    // ===== 时间戳 =====
    ClientTimestamp  int64        // 客户端发送时间
    ServerTimestamp  int64        // 服务端接收时间
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
```

### 4.3 MessagePayload 接口（多态载荷）

```go
// MessagePayload 消息载荷接口
type MessagePayload interface {
    isMessagePayload()
}

// ==============================
// 控制消息载荷
// ==============================

// AuthPayload 鉴权载荷
type AuthPayload struct {
    Token       string  // 鉴权 Token
    Platform    string  // 客户端平台 (web/iOS/Android)
    ClientVer   string  // 客户端版本
    HeartbeatInterval uint32  // 期望心跳间隔
}

func (*AuthPayload) isMessagePayload() {}

// AuthResultPayload 鉴权结果载荷
type AuthResultPayload struct {
    Code               uint32  // 0=成功，非0=失败
    Msg                string  // 状态描述
    HeartbeatInterval  uint32  // 服务端心跳间隔(秒)
    ConnectionExpire   uint64  // 连接过期时间戳
    MaxPacketSize      uint32  // 最大数据包大小
}

func (*AuthResultPayload) isMessagePayload() {}

// HeartbeatPayload 心跳载荷
type HeartbeatPayload struct {
    ClientTime uint64  // 客户端时间戳
}

func (*HeartbeatPayload) isMessagePayload() {}

// LogoutPayload 登出载荷
type LogoutPayload struct {
    Reason uint32  // 1=主动登出 2=切换账号 3=其他
}

func (*LogoutPayload) isMessagePayload() {}

// SubscribePayload 订阅载荷
type SubscribePayload struct {
    Topic string  // 订阅主题
    Qos   uint32  // QoS 等级 (0=至多一次, 1=至少一次, 2=只有一次)
}

func (*SubscribePayload) isMessagePayload() {}

// ==============================
// 业务消息载荷
// ==============================

// BusinessPayload 业务透传载荷
type BusinessPayload struct {
    MsgID   string          // 业务消息唯一 ID
    BizType BusinessType    // 业务类型 (IM/LIVE/MESSAGE/PUSH)
    RoomID  string          // 房间/群组 ID（可选，用于路由）
    Body    []byte          // 业务载荷（网关不解析）
}

func (*BusinessPayload) isMessagePayload() {}

// AckPayload 消息确认载荷（客户端 → 服务端）
type AckPayload struct {
    Acks []AckItem  // 批量 ACK
}

type AckItem struct {
    MsgID   string  // 消息 ID
    Success bool    // 处理结果
}

func (*AckPayload) isMessagePayload() {}

// ==============================
// 下行消息载荷（服务端 → 客户端）
// ==============================

// DownstreamPayload 下行消息载荷
type DownstreamPayload struct {
    MsgID      string       // 消息 ID
    BizType    BusinessType // 业务类型
    RoomID     string       // 房间/群组 ID
    RoomType   RoomType     // 房间类型
    FromUserID string       // 发送者用户 ID
    Body       []byte       // 消息体
    Qos        QosLevel     // 投递质量等级
    NeedAck    bool         // 是否需要客户端 ACK
}

type RoomType int32

const (
    RoomTypeSingle   RoomType = 1  // 单聊
    RoomTypeGroup    RoomType = 2  // 群聊
    RoomTypeLiveRoom RoomType = 3  // 直播间
)

type QosLevel int32

const (
    QosAtMostOnce  QosLevel = 0  // 至多一次（Live 消息，无需存储）
    QosAtLeastOnce QosLevel = 1  // 至少一次（IM/Push 消息，需要存储）
)

func (*DownstreamPayload) isMessagePayload() {}
```

### 4.4 业务类型定义

```go
// BusinessType 业务类型
type BusinessType int32

const (
    BusinessTypeUnknown BusinessType = 0
    BusinessTypeIM      BusinessType = 1  // 即时通讯
    BusinessTypeLIVE    BusinessType = 2  // 直播消息
    BusinessTypeMESSAGE BusinessType = 3  // 消息中心
    BusinessTypePUSH    BusinessType = 4  // 系统推送
)
```

### 4.5 业务消息元数据（JSON 格式）

这是业务层的统一消息格式，用于客户端与服务端、或者服务端之间传递：

```json
{
  "msg_id": "uuid",                    // 消息唯一 ID
  "seq_id": 12345,                     // 消息序列号（用于排序、去重）
  "from": "user_id",                   // 发送者用户 ID
  "to": "user_id | room_id",           // 接收者（用户 ID 或房间 ID）
  "msg_type": "im | live | push",      // 消息类型（业务分类）
  "room_type": "single | group | live_room",  // 房间类型
  "timestamp": 1712739200,             // 消息时间戳（秒）
  "payload": {...},                    // 业务载荷（网关透传）
  "ack_required": true,                // 是否需要客户端 ACK
  "offline_storage": true              // 是否需要离线存储
}
```

**元数据字段说明**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `msg_id` | string | 是 | 消息唯一 ID，UUID 格式 |
| `seq_id` | int64 | 是 | 序列号，用于消息排序和去重 |
| `from` | string | 是 | 发送者用户 ID |
| `to` | string | 是 | 接收者（用户 ID 或房间 ID） |
| `msg_type` | string | 是 | 业务消息类型：`im`/`live`/`push` |
| `room_type` | string | 否 | 房间类型：`single`/`group`/`live_room` |
| `timestamp` | int64 | 是 | Unix 时间戳（秒） |
| `payload` | object | 是 | 业务自定义载荷，网关透传 |
| `ack_required` | bool | 否 | 是否需要客户端 ACK（默认 false） |
| `offline_storage` | bool | 否 | 是否需要离线存储（默认 true for IM/Push） |

### 4.6 元数据与 Go 结构映射

```
业务消息 JSON（元数据）
    │
    ├── msg_id        → BusinessPayload.MsgID / DownstreamPayload.MsgID
    ├── from          → DownstreamPayload.FromUserID
    ├── to            → DownstreamPayload.RoomID（作为路由目标）
    ├── msg_type      → BusinessPayload.BizType / DownstreamPayload.BizType
    ├── room_type     → DownstreamPayload.RoomType
    ├── seq_id        → （存入离线存储，用于排序）
    ├── timestamp     → （存入离线存储）
    ├── payload       → BusinessPayload.Body / DownstreamPayload.Body
    ├── ack_required  → DownstreamPayload.NeedAck
    └── offline_storage → （根据 msg_type 决定，Live 消息为 false）
```

**示例映射**：

```go
// JSON → DownstreamPayload 映射
{
  "msg_id": "550e8400-e29b-41d4-a716-446655440000",
  "from": "user_123",
  "to": "room_456",
  "msg_type": "im",
  "room_type": "group",
  "payload": {"content": "Hello group!"},
  "ack_required": true,
  "offline_storage": true
}

// ↓ 映射为 ↓

DownstreamPayload{
    MsgID:      "550e8400-e29b-41d4-a716-446655440000",
    FromUserID: "user_123",
    RoomID:     "room_456",
    BizType:    BusinessTypeIM,
    RoomType:   RoomTypeGroup,
    Body:       []byte(`{"content": "Hello group!"}`),
    NeedAck:    true,
}
```

### 4.7 Proto 映射关系

```
ClientSignal (proto)
    │
    ├── SignalType + oneof payload
    │       │
    │       ├── heartbeat_ping → HeartbeatPayload
    │       ├── auth_request → AuthPayload
    │       ├── business_up → BusinessPayload
    │       ├── message_ack → AckPayload
    │       └── business_down → DownstreamPayload
    │
    └── request_id, sequence_id → Message.RequestID, SequenceID

DownstreamKafkaMessage (proto) → DownstreamPayload
```

### 4.8 消息类型与 QoS 策略

| 消息类型 | SignalType | QoS | 离线存储 | ACK | 适用场景 |
|----------|-----------|-----|---------|-----|----------|
| IM 消息 | BusinessUp | AtLeastOnce | 是 | 是 | 单聊、群聊 |
| Live 消息 | BusinessUp | AtMostOnce | 否 | 否 | 直播弹幕、礼物 |
| Push 消息 | BusinessDown | AtLeastOnce | 是 | 是 | 系统通知 |
| 心跳 | HeartbeatPing | - | 否 | 否 | 连接保活 |
| 鉴权 | AuthRequest | - | 否 | 是 | 认证 |

### 4.9 原有 types.Message（待废弃）

```go
// OldMessage 原有的 Message 结构（重构后废弃）
// 问题：字段混杂、Payload 弱结构、缺失 Room/Group 路由字段
type OldMessage struct {
    RequestID  string
    SequenceID uint64
    Type       gateway.SignalType
    Body       []byte
    MsgID      string
    BizType    gateway.BusinessType
    UserID     string
    DeviceID   string
    AuthToken  string
    Platform   string
    // ... 更多混杂字段
}
```

---

## 5. 接口设计

### 5.1 接口定义原则

| 接口 | 定义位置 | 实现位置 | 理由 |
|------|---------|---------|------|
| `UpstreamSender` | `connection` 包 | `worker` 包 | Connection 是消费者，Worker 是实现者 |
| `ConnectionRegistry` | `worker` 包 | `session` 包 | Worker 是消费者，Session 是实现者 |
| `Transport` | `connection` 包 | `transport` 包 | Connection 是消费者 |
| `OfflineStore` | `worker` 包 | `storage` 包 | Worker 是消费者 |

### 5.2 Transport 层接口

```go
// Transport 定义在 connection 包
type Transport interface {
    Read(ctx context.Context) (*gateway.ClientSignal, error)
    SetReadDeadline(t time.Time) error
    Write(ctx context.Context, data *gateway.ClientSignal) error
    Close() error
    RemoteAddr() string
}
```

### 5.3 Connection 层接口

```go
// ==============================
// upstream_sender.go (connection 包)
// ==============================

// UpstreamSender 提交上行业务消息的接口
// 定义在 connection 包，由 worker 包实现
type UpstreamSender interface {
    // Submit 提交上行任务
    // ctx: 上下文，用于超时和取消
    // job: 上行任务
    // 返回: SubmitResult 包含是否成功及原因
    Submit(ctx context.Context, job UpstreamJob) SubmitResult
}

// SubmitResult 上行提交结果
type SubmitResult struct {
    Accepted bool
    Reason   string  // "ok", "queue_full", "closed", "unknown_biz_type"
}

// UpstreamJob 上行任务
type UpstreamJob struct {
    // 消息内容（使用新的 Payload 设计）
    Payload  MessagePayload       // 消息载荷，支持多态

    // 传输层元信息
    RequestID  string            // 全链路追踪 ID
    SequenceID uint64            // 序列号

    // 发送方上下文
    ConnID    string            // 连接 ID
    UserID    string            // 用户 ID
    DeviceID  string            // 设备 ID
    BizType   BusinessType      // 业务类型

    // 时间戳
    Timestamp int64             // 时间戳

    // ACK 相关（从 Payload 中提取以便快速处理）
    IsAck    bool              // 是否是 ACK 消息
    AckMsgID string            // ACK 对应的消息 ID
}

// ==============================
// connection.go (connection 包)
// ==============================

// ConnectionAccessor 供 Handler 使用
type ConnectionAccessor interface {
    GetConnID() string
    SetUserInfo(userID, deviceID string)
    GetUserInfo() (userID, deviceID string)
    GetRemoteAddr() string
    GetWriteCh() chan *types.Message
    IsAuthed() bool
    SetAuthed(authed bool)
    RouterRegister(userID, deviceID string)
    RefreshRoute()
    SubmitUpstream(ctx context.Context, msg *types.Message) SubmitResult
    SubmitAck(ctx context.Context, msgID string) SubmitResult
}

// ConnectionFactory 创建连接实例
type ConnectionFactory interface {
    CreateConnection(tp Transport) *Connection
}

// MsgHandler 消息处理接口
type MsgHandler interface {
    Handle(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error
}

// HandlerRegistry 消息处理注册表
type HandlerRegistry interface {
    Register(msgType gateway.SignalType, handler MsgHandler)
    HandleMessage(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error
}
```

### 5.4 Worker 层接口

```go
// ==============================
// registry.go (worker 包)
// ==============================

// ConnectionWriter 消息写入接口
// 定义在 worker 包，由 connection 包实现
type ConnectionWriter interface {
    // Write 非阻塞写入消息到连接
    // 返回 true 表示写入成功，false 表示通道满
    Write(msg *types.Message) bool
}

// ConnectionRegistry 连接注册表接口
// 定义在 worker 包，由 session 包实现
type ConnectionRegistry interface {
    // Register 注册连接
    Register(connID string, writer ConnectionWriter)

    // Unregister 注销连接
    Unregister(connID string)

    // Get 根据 connID 获取 ConnectionWriter
    Get(connID string) (ConnectionWriter, bool)
}

// ==============================
// storage.go (worker 包)
// ==============================

// OfflineStore 离线消息存储接口
// 定义在 worker 包，由 storage 包实现
type OfflineStore interface {
    // Save 保存离线消息
    Save(ctx context.Context, msg *OfflineMessage) error

    // Get 获取用户离线消息
    Get(ctx context.Context, userID string, limit int) ([]*OfflineMessage, error)

    // MarkDelivered 标记消息已送达
    MarkDelivered(ctx context.Context, msgID string) error

    // Delete 删除离线消息
    Delete(ctx context.Context, msgID string) error

    // Count 获取用户离线消息数量
    Count(ctx context.Context, userID string) (int64, error)
}

// OfflineMessage 离线消息结构
type OfflineMessage struct {
    MsgID     string
    UserID    string
    DeviceID  string
    FromUserID string
    MsgType   gateway.BusinessType
    RoomType  gateway.RoomType
    Payload   []byte
    SeqID     int64
    Timestamp int64
    CreatedAt time.Time
}

// ==============================
// pool.go (worker 包)
// ==============================

// WorkerPoolInterface worker 池接口
type WorkerPoolInterface interface {
    Start()
    Stop()
    SubmitUpstream(ctx context.Context, job UpstreamJob) SubmitResult
    SubmitDownstream(job DownstreamJob) bool
    DeliverResult(ctx context.Context, msgID string) error  // ACK 处理
}

// UpstreamPipeline 上行管道接口（Kafka/gRPC）
type UpstreamPipeline interface {
    Send(ctx context.Context, req *types.UpstreamRequest) error
    Kind() string
}

// DownstreamConsumerInterface 下行消费者接口
type DownstreamConsumerInterface interface {
    Start(ctx context.Context) error
    Stop() error
}
```

### 5.5 Router 层接口

```go
// LocalRouterInterface 本地路由接口
type LocalRouterInterface interface {
    Register(userID, deviceID string, conn ConnectionInterface)
    Unregister(conn ConnectionInterface)
    GetByUserID(userID string) ([]ConnectionInterface, bool)
    GetByDeviceID(deviceID string) (ConnectionInterface, bool)
    Count() (userCount, deviceCount uint)
    CleanTimeout() int
}

// DistributedRouterInterface 分布式路由接口
type DistributedRouterInterface interface {
    RegisterUser(ctx context.Context, userID, deviceID string) error
    RefreshRoute(ctx context.Context, userID, deviceID string) error
    UnregisterUser(ctx context.Context, userID, deviceID string) error
}
```

---

## 6. 目录结构

```
gateway/internal/
├── config/              # 配置加载
├── types/               # 内部类型定义
│
├── transport/           # 传输层
│   ├── transport.go    # Transport 接口
│   ├── tcp.go          # TCP 实现
│   ├── websocket.go    # WebSocket 实现
│   ├── grpc.go         # gRPC 实现
│   └── mqtt.go         # MQTT 实现
│
├── connection/          # 连接层
│   ├── connection.go   # Connection 主结构
│   ├── factory.go      # ConnectionFactory
│   ├── upstream_sender.go  # UpstreamSender 接口 + SubmitResult + UpstreamJob
│   ├── handler.go      # MsgHandler + Registry
│   ├── auth.go         # AuthHandler
│   ├── heartbeat.go    # HeartbeatHandler
│   ├── upstream.go     # UpstreamHandler
│   ├── ack.go          # AckHandler (新增)
│   └── downstream.go   # DownstreamHandler
│
├── worker/              # Worker 层
│   ├── pool.go         # WorkerPool
│   ├── registry.go     # ConnectionRegistry 接口 + ConnectionWriter 接口
│   ├── manager.go      # WorkerManager
│   ├── storage/
│   │   ├── interface.go # OfflineStore 接口
│   │   ├── mysql.go    # MySQL 实现
│   │   └── redis.go    # Redis 缓存实现
│   ├── upstream/
│   │   ├── sender.go   # UpstreamPipeline 接口
│   │   └── kafka.go    # KafkaPipeline 实现
│   └── downstream/
│       └── consumer.go # DownstreamConsumer
│
├── router/              # 路由层
│   ├── local.go        # LocalRouter
│   ├── distributed.go  # DistributedRouter
│   └── interfaces.go   # 路由接口定义
│
├── session/             # 协调层
│   ├── session.go      # Session 主结构
│   ├── options.go      # 配置选项
│   └── accessor.go    # Session 访问接口
│
├── server/              # HTTP/入口
│   └── server.go
│
└── main.go
```

---

## 7. 消息流详解

### 7.1 上行消息流（普通业务消息）

```
Client  ──►  Transport  ──►  Connection.ReadLoop
                                     │
                                     │ Decode
                                     ▼
                              types.Message
                                     │
                                     │ Handler 调度
                                     ▼
                            UpstreamHandler.Handle()
                                     │
                                     │ SubmitUpstream(ctx, msg)
                                     ▼
                           UpstreamSender.Submit()
                           (connection 层接口)
                                     │
                                     │ Session 注入的实现
                                     ▼
                           WorkerPool.SubmitUpstream()
                                     │
                                     │ 写入 upstreamCh
                                     ▼
                          UpstreamWorker.Send()
                                     │
                                     │ 发送到消息队列
                                     ▼
                          Kafka (upstream topic)
```

### 7.2 上行消息流（ACK 消息）

```
Client  ──►  Transport  ──►  Connection.ReadLoop
                                     │
                                     │ Decode
                                     ▼
                              types.Message
                                     │
                                     │ 判断是 ACK 消息
                                     ▼
                            AckHandler.Handle()
                                     │
                                     │ SubmitAck(ctx, msgID)
                                     ▼
                           UpstreamSender.Submit()
                           (IsAck=true, AckMsgID=msgID)
                                     │
                                     │ Worker.DeliverResult(ctx, msgID)
                                     ▼
                           OfflineStore.MarkDelivered()
                           OfflineStore.Delete()
                                     │
                                     ▼
                          更新 seq_id 到 Redis
```

### 7.3 下行消息流（在线用户）

```
Business  ──►  Kafka (downstream topic)
                               │
                               │ FetchMessage
                               ▼
                    DownstreamConsumer.Consume()
                               │
                               │ SubmitDownstream(job)
                               ▼
                    WorkerPool.downstreamCh
                               │
                               │ downstreamWorker 获取 job
                               ▼
                    DownstreamWorker.Route()
                               │
                               │ 1. 检查用户是否在线
                               │ 2. 在线：registry.Get(connID)
                               │ 3. 离线：存储离线消息
                               ▼
                    ┌─────────────────────────┐
                    │  用户在线？              │
                    └───────────┬─────────────┘
                       YES       │       NO
                         │                │
                         ▼                ▼
              ConnectionRegistry.Get()  OfflineStore.Save()
                         │                │
                         ▼                │
              ConnectionWriter.Write()    │
                         │                │
                         ▼                ▼
                    Connection.Write(msg)
                               │
                               │ 写入 WriteCh
                               ▼
                    Connection.WriteLoop
                               │
                               │ Encode
                               ▼
                    Transport.Write()  ──►  Client
```

### 7.4 下行消息流（离线用户消息投递）

```
用户上线  ──►  Connection.ReadLoop
                      │
                      │ 鉴权成功
                      ▼
            Session.HandleConnection()
                      │
                      │ 获取离线消息
                      ▼
            OfflineStore.Get(userID)
                      │
                      │ 逐条投递
                      ▼
            Connection.Write(msg)
                      │
                      │ 等待 ACK
                      ▼
            OfflineStore.Delete(msgID)  (收到 ACK 后)
```

---

## 8. 核心实现

### 8.1 WorkerManager (worker/manager.go)

```go
// WorkerManager 统一管理所有 Worker 组件
type WorkerManager struct {
    cfg          *config.Config
    distRouter   router.DistributedRouterInterface

    // 连接注册表（实现 ConnectionRegistry 接口）
    // 实际由 Session 注入，这里持有接口
    connRegistry ConnectionRegistry

    // 离线存储（实现 OfflineStore 接口）
    offlineStore OfflineStore

    // 每个 bizType 的 WorkerPool
    pools map[gateway.BusinessType]WorkerPoolInterface

    // 下行消费者
    downstreamConsumer DownstreamConsumerInterface
}

// NewWorkerManager 创建 WorkerManager
func NewWorkerManager(cfg *config.Config, dr router.DistributedRouterInterface, store OfflineStore) *WorkerManager {
    wm := &WorkerManager{
        cfg:          cfg,
        distRouter:   dr,
        offlineStore: store,
        pools:        make(map[gateway.BusinessType]WorkerPoolInterface),
    }
    return wm
}

// SetConnectionRegistry 设置连接注册表
// 由 Session 在创建后注入
func (wm *WorkerManager) SetConnectionRegistry(reg ConnectionRegistry) {
    wm.connRegistry = reg
}

// CreatePools 创建所有 WorkerPool
func (wm *WorkerManager) CreatePools() error {
    for bizType := range wm.cfg.Upstream.Kafka.BusinessTopics {
        sender := NewKafkaPipeline(...)
        pool := NewWorkerPool(wm.cfg, bizType, sender, wm.connRegistry, wm.offlineStore)
        wm.pools[bizType] = pool
    }

    wm.downstreamConsumer = NewDownstreamConsumer(wm.cfg, wm.pools, wm.connRegistry)
    return nil
}
```

### 8.2 WorkerPool (worker/pool.go)

```go
// WorkerPool 上行/下行 Worker 池
type WorkerPool struct {
    bizType gateway.BusinessType

    // 上行
    upstreamCh   chan UpstreamJob
    upstreamPipe UpstreamPipeline

    // 下行
    downstreamCh chan DownstreamJob

    // 离线存储
    offlineStore OfflineStore

    // 连接注册表
    connRegistry ConnectionRegistry

    // 生命周期
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

// SubmitUpstream 提交上行任务
func (p *WorkerPool) SubmitUpstream(ctx context.Context, job UpstreamJob) SubmitResult {
    job.Ctx = ctx
    job.Timestamp = time.Now().UnixMilli()

    // 处理 ACK 消息
    if job.IsAck {
        return p.handleAck(ctx, job.AckMsgID)
    }

    select {
    case p.upstreamCh <- job:
        return SubmitResult{Accepted: true, Reason: "ok"}
    case <-ctx.Done():
        return SubmitResult{Accepted: false, Reason: "context_cancelled"}
    default:
        return SubmitResult{Accepted: false, Reason: "queue_full"}
    }
}

// handleAck 处理 ACK 消息
func (p *WorkerPool) handleAck(ctx context.Context, msgID string) SubmitResult {
    if p.offlineStore == nil {
        return SubmitResult{Accepted: true, Reason: "no_offline_store"}
    }

    // 1. 标记已送达
    if err := p.offlineStore.MarkDelivered(ctx, msgID); err != nil {
        logger.Error("mark delivered failed", zap.String("msg_id", msgID), zap.Error(err))
    }

    // 2. 删除离线消息
    if err := p.offlineStore.Delete(ctx, msgID); err != nil {
        logger.Error("delete offline msg failed", zap.String("msg_id", msgID), zap.Error(err))
    }

    return SubmitResult{Accepted: true, Reason: "ok"}
}

// SubmitDownstream 提交下行任务
func (p *WorkerPool) SubmitDownstream(job DownstreamJob) bool {
    select {
    case p.downstreamCh <- job:
        return true
    default:
        return false
    }
}
```

### 8.3 DownstreamWorker (worker/downstream/worker.go)

`DownstreamWorker` 负责处理下行消息的路由和投递，包括在线用户直接投递和离线用户存储。

```go
// DownstreamWorker 下行 Worker
type DownstreamWorker struct {
    id           int
    bizType      gateway.BusinessType
    downstreamCh chan DownstreamJob
    connRegistry ConnectionRegistry    // 用户连接注册表
    offlineStore OfflineStore          // 离线消息存储
}

// downstreamWorker 下行处理 goroutine
func (w *DownstreamWorker) run(pool *WorkerPool) {
    defer pool.wg.Done()

    for {
        select {
        case <-pool.ctx.Done():
            logger.Debug("downstream worker stopped",
                zap.Int("worker_id", w.id),
                zap.String("biz_type", w.bizType.String()))
            return
        case job, ok := <-w.downstreamCh:
            if !ok {
                return
            }
            w.route(job)
        }
    }
}

// route 路由下行消息
func (w *DownstreamWorker) route(job DownstreamJob) {
    msg := job.DownstreamMsg

    // 1. 根据 bizType 获取 QoS 策略
    qos := getQosLevel(msg.BusinessType)
    needAck := needAck(msg.BusinessType)

    // 2. 构建下行消息
    downstreamMsg := w.buildDownstreamMessage(msg, qos, needAck)

    // 3. 获取用户连接
    writer, ok := w.connRegistry.Get(msg.ToUserId)
    if !ok {
        // 4. 用户离线，处理离线存储
        w.handleOffline(msg, qos)
        return
    }

    // 5. 用户在线，尝试投递
    if !writer.Write(downstreamMsg) {
        // 6. 写入失败（channel full），存入离线
        logger.Warn("downstream write channel full, store offline",
            zap.String("user_id", msg.ToUserId),
            zap.String("msg_id", msg.MsgId))
        w.handleOffline(msg, qos)
    } else {
        logger.Debug("downstream message routed",
            zap.String("user_id", msg.ToUserId),
            zap.String("msg_id", msg.MsgId),
            zap.Bool("need_ack", needAck))
    }
}

// handleOffline 处理离线消息
func (w *DownstreamWorker) handleOffline(msg *gateway.DownstreamKafkaMessage, qos QosLevel) {
    // Live 消息不需要离线存储
    if qos == QosAtMostOnce {
        logger.Debug("live message, skip offline storage",
            zap.String("msg_id", msg.MsgId))
        return
    }

    // 构建离线消息
    offlineMsg := &OfflineMessage{
        MsgID:     msg.MsgId,
        UserID:    msg.ToUserId,
        DeviceID:  msg.DeviceId,
        FromUserID: msg.FromUserId,
        MsgType:   msg.BusinessType,
        RoomType:  msg.RoomType,
        Payload:   msg.Payload,
        SeqID:     msg.SeqId,
        Timestamp: msg.Timestamp,
    }

    // 存储离线消息
    if err := w.offlineStore.Save(context.Background(), offlineMsg); err != nil {
        logger.Error("store offline message failed",
            zap.String("msg_id", msg.MsgId),
            zap.String("user_id", msg.ToUserId),
            zap.Error(err))
    } else {
        logger.Debug("offline message stored",
            zap.String("msg_id", msg.MsgId),
            zap.String("user_id", msg.ToUserId))
    }
}

// buildDownstreamMessage 构建下行消息
func (w *DownstreamWorker) buildDownstreamMessage(
    msg *gateway.DownstreamKafkaMessage,
    qos QosLevel,
    needAck bool,
) *Message {
    return &Message{
        Type: gateway.SignalType_SIGNAL_TYPE_BUSINESS_DOWN,
        Payload: &DownstreamPayload{
            MsgID:      msg.MsgId,
            BizType:    msg.BusinessType,
            RoomID:     msg.ToUserId,
            RoomType:   msg.RoomType,
            FromUserID: msg.FromUserId,
            Body:       msg.Payload,
            Qos:        qos,
            NeedAck:    needAck,
        },
        Metadata: map[string]string{
            "correlation_id": msg.CorrelationId,
        },
    }
}

// getQosLevel 根据业务类型获取 QoS 级别
func getQosLevel(bizType gateway.BusinessType) QosLevel {
    switch bizType {
    case gateway.BusinessType_BUSINESS_TYPE_LIVE:
        return QosAtMostOnce
    default:
        return QosAtLeastOnce
    }
}

// needAck 是否需要客户端 ACK
func needAck(bizType gateway.BusinessType) bool {
    switch bizType {
    case gateway.BusinessType_BUSINESS_TYPE_LIVE:
        return false
    default:
        return true
    }
}
```

**DownstreamWorker 流程图**：

```
Kafka DownstreamMsg
        │
        ▼
┌─────────────────────────┐
│ DownstreamWorker.route() │
└───────────┬─────────────┘
            │
            ▼
    ┌───────────────┐
    │ 获取 QoS 策略   │
    │ (AtMostOnce /  │
    │  AtLeastOnce)  │
    └───────┬───────┘
            │
            ▼
    ┌───────────────────────┐
    │ connRegistry.Get(     │
    │   msg.ToUserId)       │
    └───────┬───────────────┘
            │
     ┌──────┴──────┐
     │  用户在线？   │
     └──────┬──────┘
       YES  │  NO
      ┌─────┴─────┐
      ▼          ▼
┌──────────┐  ┌─────────────────┐
│ writer.  │  │ handleOffline() │
│ Write()  │  │                 │
└─────┬────┘  │ ┌─────────────┐ │
      │       │ │ QoS=AtMost  │ │
 ┌────┴────┐  │ │ → 跳过存储  │ │
 │写入失败？ │ │ └─────────────┘ │
 └────┬────┘  │ │ 其他 → Save() │
  YES │       │ └───────────────┘ │
 ┌────┴────┐  └───────────────────┘
 ▼         │
┌──────────┐│
│handle    ││
│Offline() │
└──────────┘
```

### 8.4 DownstreamConsumer (worker/downstream/consumer.go)

```go
// DownstreamConsumer 下行消息消费者
type DownstreamConsumer struct {
    cfg     *config.KafkaConfig
    pools   map[gateway.BusinessType]WorkerPoolInterface
    registry worker.ConnectionRegistry
    wg      sync.WaitGroup
}

// Consume 消费下行消息
func (c *DownstreamConsumer) Consume(msg *gateway.DownstreamKafkaMessage) {
    job := DownstreamJob{
        Msg:    msg,
        Offset: msg.Offset,
    }

    // 获取用户连接
    writer, ok := c.registry.Get(msg.ToUserId)
    if !ok {
        // 用户离线，存储离线消息
        c.storeOffline(msg)
        return
    }

    // 用户在线，提交到 worker pool 投递
    if !c.pools[msg.BusinessType].SubmitDownstream(job) {
        // 投递失败，存入离线
        c.storeOffline(msg)
    }
}

// storeOffline 存储离线消息
func (c *DownstreamConsumer) storeOffline(msg *gateway.DownstreamKafkaMessage) {
    offlineMsg := &OfflineMessage{
        MsgID:     msg.MsgId,
        UserID:    msg.ToUserId,
        DeviceID:  msg.DeviceId,
        FromUserID: msg.FromUserId,
        MsgType:   msg.BusinessType,
        RoomType:  msg.RoomType,
        Payload:   msg.Payload,
        SeqID:     msg.SeqId,
        Timestamp: msg.Timestamp,
    }

    // 根据消息类型决定是否存储（IM 和 Push 存储，Live 不存储）
    if msg.BusinessType == gateway.BusinessType_BUSINESS_TYPE_LIVE {
        return // Live 消息不需要离线存储
    }

    if err := c.offlineStore.Save(context.Background(), offlineMsg); err != nil {
        logger.Error("store offline msg failed", zap.String("msg_id", msg.MsgId), zap.Error(err))
    }
}
```

### 8.5 Session (session/session.go)

```go
// Session 协调层
type Session struct {
    cfg        *config.Config

    // 路由层
    localRouter       *router.LocalRouter
    distributedRouter *router.DistributedRouter

    // Worker 管理器
    workerManager *worker.WorkerManager

    // 连接工厂
    connectionFactory *connection.ConnectionFactory

    // HTTP 服务
    httpServer *http.Server

    // 连接计数
    connCount uint32
}

// NewSession 创建 Session
func NewSession(cfg *config.Config) *Session {
    s := &Session{cfg: cfg}

    // 初始化路由
    s.localRouter = router.NewLocalRouter()
    s.distributedRouter = router.NewDistributedRouter(...)

    // 初始化离线存储
    offlineStore := storage.NewMySQLStore(cfg.Database)

    // 初始化 WorkerManager
    s.workerManager = worker.NewWorkerManager(cfg, s.distributedRouter, offlineStore)

    // 初始化 ConnectionRegistry（Session 实现 worker.ConnectionRegistry 接口）
    registry := NewConnectionRegistry()
    s.workerManager.SetConnectionRegistry(registry)

    // 创建 WorkerPool
    s.workerManager.CreatePools()

    // 初始化 ConnectionFactory
    s.connectionFactory = connection.NewConnectionFactory(
        s.localRouter,
        s.distributedRouter,
        s.workerManager.GetUpstreamSender(),
    )

    return s
}

// HandleConnection 处理新连接
func (s *Session) HandleConnection(tp transport.Transport) {
    conn := s.connectionFactory.CreateConnection(tp)

    // 注册到 Worker 的 ConnectionRegistry
    s.workerManager.GetConnectionRegistry().Register(conn.GetConnID(), conn)

    // 用户上线时投递离线消息
    if conn.IsAuthed() {
        s.deliverOfflineMessages(conn)
    }

    // 启动读写循环
    go conn.WriteLoop()
    conn.ReadLoop()

    // 清理
    s.workerManager.GetConnectionRegistry().Unregister(conn.GetConnID())
}

// deliverOfflineMessages 投递离线消息
func (s *Session) deliverOfflineMessages(conn *connection.Connection) {
    userID, deviceID := conn.GetUserInfo()

    messages, err := s.workerManager.GetOfflineStore().Get(context.Background(), userID, 100)
    if err != nil {
        logger.Error("get offline messages failed", zap.String("user_id", userID), zap.Error(err))
        return
    }

    for _, msg := range messages {
        if !conn.Write(msg) {
            logger.Warn("write offline msg failed, channel full", zap.String("msg_id", msg.MsgID))
            break
        }
    }

    logger.Info("delivered offline messages", zap.String("user_id", userID), zap.Int("count", len(messages)))
}

// ConnectionRegistry Session 实现的连接注册表
type ConnectionRegistry struct {
    conns map[string]worker.ConnectionWriter
    mux   sync.RWMutex
}

func (r *ConnectionRegistry) Register(connID string, writer worker.ConnectionWriter) {
    r.mux.Lock()
    defer r.mux.Unlock()
    r.conns[connID] = writer
}

func (r *ConnectionRegistry) Unregister(connID string) {
    r.mux.Lock()
    defer r.mux.Unlock()
    delete(r.conns, connID)
}

func (r *ConnectionRegistry) Get(connID string) (worker.ConnectionWriter, bool) {
    r.mux.RLock()
    defer r.mux.RUnlock()
    w, ok := r.conns[connID]
    return w, ok
}
```

### 8.6 AckHandler (connection/ack.go)

```go
// AckHandler 处理客户端 ACK
type AckHandler struct{}

func (h *AckHandler) Handle(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error {
    // 解析 ACK 消息
    ack := &types.AckMessage{}
    if err := proto.Unmarshal(msg.Body, ack); err != nil {
        logger.Error("unmarshal ack failed", zap.Error(err))
        return err
    }

    // 提交 ACK 到 worker 处理
    return conn.SubmitAck(ctx, ack.MsgId)
}

// ConnectionAccessor.SubmitAck 提交 ACK
func (c *Connection) SubmitAck(ctx context.Context, msgID string) SubmitResult {
    if c.upstreamSender == nil {
        return SubmitResult{Accepted: false, Reason: "not_initialized"}
    }

    job := UpstreamJob{
        ConnID:    c.connID,
        UserID:    c.userID,
        DeviceID:  c.deviceID,
        BizType:   gateway.BusinessType_BUSINESS_TYPE_IM, // ACK 统一走 IM 通道
        IsAck:     true,
        AckMsgID:  msgID,
    }

    return c.upstreamSender.Submit(ctx, job)
}
```

---

## 9. Redis 数据结构

| Key 模式 | 类型 | 说明 | TTL |
|----------|------|------|-----|
| `user:route:{userId}` | String | 用户路由：gatewayNodeId:connId | 心跳×2 |
| `gateway:conns:{nodeId}` | Set | 节点会话集合 | 无 |
| `user:online:{userId}` | String | 用户在线状态：1/0 | 心跳×2 |
| `room:online:{roomId}` | Set | 房间在线用户集合 | 无 |
| `msg:seq:{userId}` | String | 用户当前 seq_id | 1小时 |
| `msg:delivered:{msgId}` | String | 消息送达标记 | 1小时 |
| `ratelimit:{userId}:{action}` | String | 限流计数器 | 1分钟 |
| `channel:user:{userId}` | PubSub | 用户消息投递通知频道 | - |

---

## 10. 接口依赖关系图

```
                                    ┌─────────────────────────────────────┐
                                    │           session (协调层)           │
                                    │                                     │
                                    │  · 持有 LocalRouter                 │
                                    │  · 持有 DistributedRouter           │
                                    │  · 持有 WorkerManager                │
                                    │  · 实现 ConnectionRegistry 接口     │
                                    │  · 持有 ConnectionFactory           │
                                    └─────────────────────────────────────┘
                                                    ▲
                                                    │
                          ┌─────────────────────────┼─────────────────────────┐
                          │                         │                         │
                          ▼                         │                         ▼
         ┌─────────────────────────────────┐       │    ┌─────────────────────────────────┐
         │        connection (连接层)       │       │    │          worker (业务层)        │
         │                                  │       │    │                                  │
         │  · 定义 UpstreamSender 接口      │       │    │  · 实现 UpstreamSender 接口      │
         │  · 持有 UpstreamSender (注入)    │       │    │  · 定义 ConnectionRegistry 接口  │
         │  · 实现 ConnectionWriter 接口    │       │    │  · 持有 ConnectionRegistry      │
         │  · 定义 OfflineStore 接口        │       │    │    (由 Session 注入)              │
         │                                  │       │    │  · 实现 OfflineStore 接口        │
         │                                  │       │    │  · 持有 OfflineStore             │
         └─────────────────────────────────┘       │    └─────────────────────────────────┘
                                                    │
                                                    │     Session 是连接点
```

**依赖方向**：
- `connection` 包 → `worker` 包（通过 UpstreamSender 接口）
- `worker` 包 → `session` 包（通过 ConnectionRegistry 接口）
- `connection` 包和 `worker` 包无直接依赖

---

## 11. 设计决策总结

| 决策 | 选择 | 理由 |
|------|------|------|
| UpstreamSender 接口定义 | `connection` 包 | 消费方定义接口，实现方依赖接口 |
| ConnectionRegistry 接口定义 | `worker` 包 | 消费方定义接口，实现方依赖接口 |
| Session 协调 | 作为桥梁 | Connection 和 Worker 通过 Session 间接交互，无直接依赖 |
| 离线消息存储 | MySQL + Redis 缓存 | IM 核心功能，热点数据缓存加速 |
| 消息送达保证 | At-least-once | 实现复杂度适中，业务可接受 |
| ACK 处理 | 异步处理 | 不阻塞主流程 |
| Live 消息 | At-most-once | 无需存储，直接转发即可 |

---

## 12. 验收标准

1. **无循环依赖**: `connection` 和 `worker` 包之间无直接 import 依赖
2. **接口清晰**: 每层只依赖接口，不依赖具体实现
3. **可测试性**: 每层可以独立测试（mock 接口）
4. **功能不变**: 重构后行为与重构前一致
5. **编译通过**: 无循环 import 错误
6. **ACK 机制**: 客户端 ACK 能正确触发离线消息清理
7. **离线存储**: 用户离线时消息能正确存储，上线时正确投递

---

## 13. 附录

### 13.1 核心接口一览

| 接口 | 定义位置 | 实现位置 | 用途 |
|------|---------|---------|------|
| `Transport` | `connection` | `transport` | 纯 I/O 操作 |
| `UpstreamSender` | `connection` | `worker` | 提交上行业务消息 |
| `ConnectionWriter` | `worker` | `connection` | 写入下行消息到连接 |
| `ConnectionRegistry` | `worker` | `session` | 管理 connID → ConnectionWriter 映射 |
| `OfflineStore` | `worker` | `storage` | 离线消息存取 |
| `MsgHandler` | `connection` | `connection` | 处理不同类型的消息 |
| `UpstreamPipeline` | `worker` | `worker` | Kafka/gRPC 发送 |

### 13.2 待实施项

- [ ] gRPC 传输层实现细节
- [ ] MQTT 传输层实现细节
- [ ] 连接池复用策略
- [ ] 监控指标集成方式
- [ ] 优雅关闭的详细流程
- [ ] MySQL 离线存储实现
- [ ] Redis 缓存实现
- [ ] ACK 超时重试机制
- [ ] seq_id 统一管理

---

## 14. 参考文档

- [2026-04-10-im-gateway-design.md](../../test/docs/superpowers/specs/2026-04-10-im-gateway-design.md)

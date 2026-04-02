# long-gw

长连接接入层网关，支持单节点百万级长连接。

## 技术栈

- **语言**: Go
- **框架**: Gin (HTTP控制层)、net/http、google.golang.org/grpc
- **协议**: TCP、WebSocket、gRPC、QUIC
- **消息队列**: Kafka
- **缓存/路由**: Redis Cluster

## 项目目录

```
gateway/          # 接入层服务（核心长连接服务）
auth/             # auth 控制层服务
sdk/              # 客户端SDK（go-sdk）
cmd/              # 入口命令
internal/         # 内部包
  ├── config/     # 配置
  ├── protocol/   # 协议处理
  ├── router/     # 路由中心
  └── ...
```

## 架构设计

整体架构由四部分组成：

```
┌─────────────────────────────────────────────────────────────────┐
│                        客户端                                   │
│  ┌─────────────┐                                                │
│  │  统一SDK    │ ← 业务SDK请求                                  │
│  └──────┬──────┘                                                │
└─────────┼───────────────────────────────────────────────────────┘
          │ 控制层API获取token/接入点
┌─────────▼───────────────────────────────────────────────────────┐
│                        控制层 (Gin HTTP)                         │
│  - Token生成与验证                                               │
│  - 接入点下发                                                    │
│  - 流量控制                                                      │
└─────────┬───────────────────────────────────────────────────────┘
          │ gRPC
┌─────────▼───────────────────────────────────────────────────────┐
│                        接入层 (长连接核心)                        │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐             │
│  │ Connection层 │  │  Session层  │  │ LocalRouter │             │
│  │ (协议封装)   │  │ (业务逻辑)  │  │ (本地路由)  │             │
│  └─────────────┘  └─────────────┘  └─────────────┘             │
└─────────┬───────────────────────────────────────────────────────┘
          │ gRPC / Kafka
┌─────────▼───────────────────────────────────────────────────────┐
│                        路由层 (Redis Cluster)                    │
│  - 用户→节点映射                                                 │
│  - 设备→节点映射                                                 │
│  - 节点列表管理                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 1. 统一长连接SDK（客户端）

- 请求控制层获取token、接入点、接入协议
- 与接入层建立并维护长连接，支持断线重连
- 转发业务SDK请求到服务端
- 接收服务端下行数据并转发给业务SDK

### 2. 控制层

- 生产和验证设备合法性token
- 根据客户端属性下发接入点
- 流量控制策略

### 3. 接入层（核心）

**Connection层**：封装不同通讯协议差异，提供统一接口
- TCP: go net包
- WebSocket: gorilla/websocket
- gRPC: net/http + google.golang.org/grpc
- QUIC: quic-go

**Session层**：长连接业务逻辑，维护连接状态机
- 连接管理：连接ID ↔ 连接信息映射
- 组管理：组ID ↔ 连接信息映射
- 上行转发：业务请求 → Kafka/后端服务
- 下行推送：接收推送请求 → 写入连接

### 4. 路由层

- 设备标识 ↔ 连接标识映射（Redis存储）
- 集群节点注册与发现

## 核心流程

```
1. 建连: SDK → 控制层获取token/接入点 → 接入层建立长连接
2. 维持: SDK定时心跳(间隔30s) → 刷新路由TTL
3. 上行: 业务SDK → SDK → 接入层 → Kafka → 业务后端
4. 下行: 业务后端 → 路由层查询节点 → gRPC调用接入层 → SDK → 业务SDK
```

## 关键设计

### 协议封装

```go
// Message 统一应用层消息
type Message struct {
    MsgID   string // 消息ID
    Type    uint8  // 0x01心跳 0x02上行 0x03下行
    Body    []byte
    UserID  string // 鉴权后赋值
}

// Conn 长连接抽象接口
type Conn interface {
    ReadMessage() (*Message, error)
    WriteMessage(*Message) error
    Close() error
    RemoteAddr() string
    SetUserID(string)
    GetUserID() string
}

// ProtocolHandler 协议处理器
type ProtocolHandler interface {
    Accept(rawConn interface{}) (Conn, error)
    Name() string
}
```

### 双Goroutine模型

每个连接启动独立的readLoop和writeLoop：
- readLoop: 读取数据、解析协议、分发消息
- writeLoop: 从WriteCh读取数据、协议封装、写入
- 严格分离读写，解决TCP并发写安全问题

Worker池设计：
- 上行Worker池：处理上行请求
- 下行Worker池：处理下行推送
- 可按业务划分多个Worker池

### 本地路由中心

```go
type Connection struct {
    UserID     string
    DeviceID   string
    WriteCh    chan []byte
    LastActive time.Time
    mu         sync.RWMutex
}

type LocalRouter struct {
    mu              sync.RWMutex
    userIDToConns   map[string][]*Connection   // 用户→连接(多端)
    deviceIDToConn  map[string]*Connection     // 设备→连接(单端)
    hotUserCache    sync.Map                   // 热点用户缓存
}
```

功能：
- 注册连接：上线/鉴权后调用，清理旧连接
- 注销连接：下线/关闭时调用
- 查询连接：下行推送时调用
- 超时清理：定期清理LastActive超时的连接

### 分布式路由中心

Redis存储结构：

| Key格式 | Value | TTL | 说明 |
|---------|-------|-----|------|
| gateway:user:{userID} | 节点gRPC地址 | 120s | 用户路由 |
| gateway:device:{deviceID} | 节点gRPC地址 | 120s | 设备路由 |
| gateway:nodes | 节点地址集合 | - | 集群管理 |

```go
type DistributedRouter struct {
    redisClient  *redis.ClusterClient
    gatewayAddr  string // 当前节点gRPC地址
}
```

注册/刷新/注销与心跳绑定。

### 关键参数

| 参数 | 值 | 说明 |
|------|-----|------|
| 心跳间隔 | 30s | 客户端定时发送 |
| 心跳超时 | 90s | 3次心跳未响应则断开 |
| 路由TTL | 120s | 3次超时+缓冲 |
| 多端登录上限 | 5-10个 | 单用户最大在线设备数 |
| 单节点容量 | 100万连接 | 设计目标 |

### 高可用设计

- 接入层：无状态节点，水平扩展
- Redis：3主3从+哨兵
- Kafka：多分区多副本
- 优雅关闭：接收关闭信号后停止接收新连接，处理完存量后退出

## 消息队列

- **上行**: 接入层 → Kafka → 业务后端订阅消费
- **下行**: 业务后端 → Kafka → 接入层订阅 → 推送至客户端
- **gRPC推送**: 支持业务后端直接gRPC调用接入层推送

## 监控指标

- 连接数（总在线、单节点）
- 消息量（上行/下行QPS）
- 心跳成功率
- 响应延迟P99
- Kafka消费延迟

## 注意点
完整生命周期管控：鉴权、心跳保活、路由注册、优雅关闭、异常兜底全链路覆盖
生产级安全兜底：panic 捕获、超时控制、资源防泄漏、限流防护
日志框架，使用 slog，需封装一下，底层可以基于 zap

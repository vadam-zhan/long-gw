# long-gw

高性能长连接接入层网关，支持单节点百万级长连接。

## 项目架构

```
long-gw/              # Go Workspace 根目录
├── auth/             # 控制层服务 (Gin HTTP)
├── business/         # 业务后端服务 (独立部署)
├── gateway/          # 接入层服务 (长连接核心)
├── sdk/              # 统一客户端SDK
└── common-protocol/ # 公共协议定义 (Proto)
```

## 技术栈

- **语言**: Go 1.25+
- **HTTP框架**: Gin
- **协议**: TCP、WebSocket、gRPC、QUIC
- **消息队列**: Kafka
- **缓存/路由**: Redis

## 服务交互架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              客户端 (SDK)                                   │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │                          统一SDK                                     │  │
│  │  · TCP / WebSocket / gRPC 多协议支持                                 │  │
│  │  · 自动重连、心跳维护                                                │  │
│  │  · 消息加密、压缩                                                   │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ HTTP (获取Token/接入点)
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                              控制层 (Auth)                                  │
│  · Token 生成与验证                                                       │
│  · 接入点下发                                                             │
│  · 流量控制                                                               │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ gRPC (建立长连接)
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                              接入层 (Gateway)                               │
│                                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐     │
│  │  TCP接入    │  │ WebSocket   │  │  gRPC接入   │  │  QUIC接入   │     │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘     │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │                         Session 层                                    │  │
│  │  · 连接管理 (Connection)                                              │  │
│  │  · 本地路由 (LocalRouter)                                             │  │
│  │  · 双Goroutine模型 (readLoop/writeLoop)                              │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │                       Worker Pool                                    │  │
│  │  · 上行Worker池 (处理客户端→服务端消息)                                │  │
│  │  · 下行Worker池 (处理服务端→客户端消息)                                │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │                       Kafka Producer/Consumer                        │  │
│  │  · 上行: 消息 → Kafka (upstream topic)                               │  │
│  │  · 下行: 订阅 Kafka (downstream topic) → 推送客户端                  │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ Kafka
                    ┌───────────────┴───────────────┐
                    │                               │
                    ▼                               ▼
┌─────────────────────────────────┐  ┌─────────────────────────────────────┐
│         业务后端 (Business)      │  │           分布式路由 (Redis)          │
│                                 │  │                                     │
│  · 消费 upstream 消息            │  │  · 用户 → 节点映射                   │
│  · 业务逻辑处理                  │  │  · 设备 → 节点映射                   │
│  · 生产 downstream 消息           │  │  · 集群节点管理                      │
│  · HTTP API (推送/批量推送)      │  │                                     │
└─────────────────────────────────┘  └─────────────────────────────────────┘
```

## 数据流

### 上行消息 (Client → Business)

```
SDK → Gateway → Kafka(upstream topic) → Business消费 → 业务处理
```

### 下行消息 (Business → Client)

```
Business → Kafka(downstream topic) → Gateway订阅 → 路由查找 → SDK → 客户端
```

## 消息队列 Topic 映射

| 业务类型 | Upstream Topic | Downstream Topic |
|---------|----------------|------------------|
| im | gateway-im-upstream | gateway-im-downstream |
| live | gateway-live-upstream | gateway-live-downstream |
| message | gateway-message-upstream | gateway-message-downstream |

## 服务启动

```bash
# 启动 Auth 控制层服务
cd auth && go run main.go

# 启动 Gateway 接入层服务
cd gateway && go run main.go

# 启动 Business 业务后端服务
cd business && make build && ./bin/business
```

## 项目详情

### SDK (`sdk/`)

统一客户端SDK，支持多种协议：
- `tcp/client.go` - TCP长连接客户端
- `ws/client.go` - WebSocket客户端
- `grpc/client.go` - gRPC客户端

### Auth (`auth/`)

控制层服务，Gin HTTP服务：
- Token管理
- 接入点下发
- 流量控制

### Gateway (`gateway/`)

接入层核心服务：
- 多协议接入 (TCP/WebSocket/gRPC/QUIC)
- Session管理
- 本地路由
- Kafka消息生产和消费
- Worker池处理

### Business (`business/`)

业务后端服务：
- 独立部署
- Kafka消费者（消费上行消息）
- Kafka生产者（发送下行消息）
- HTTP API (推送/批量推送)

### Common Protocol (`common-protocol/`)

Protobuf协议定义：
- `kafka_message.proto` - Kafka消息格式
- `gateway.proto` - 网关协议
- `signal.proto` - 信令定义

## 核心设计

### 双Goroutine模型

每个连接独立的读写分离：
- `readLoop`: 读取数据、解析协议、分发消息
- `writeLoop`: 从通道读取数据、协议封装、写入

### Worker池

- 上行Worker池：处理客户端→服务端消息
- 下行Worker池：处理服务端→客户端消息

### 分布式路由

Redis存储：
| Key | Value | TTL |
|-----|-------|-----|
| gateway:user:{userID} | 节点gRPC地址 | 120s |
| gateway:device:{deviceID} | 节点gRPC地址 | 120s |

## 关键参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| gateway.addr | :8080 | 接入层监听地址 |
| auth.addr | :8081 | 控制层监听地址 |
| business.addr | :8082 | 业务服务监听地址 |
| kafka.brokers | localhost:9092 | Kafka集群 |
| redis.addr | localhost:6379 | Redis地址 |
| route.ttl | 120s | 路由过期时间 |

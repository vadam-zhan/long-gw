# Gateway 分层重构设计方案

**日期**: 2026-04-10
**状态**: 已批准
**目标**: 分离传输层、连接层、业务层，让 Session 只负责协调

---

## 1. 背景与问题

### 1.1 当前问题

Session (`session/session.go`) 当前承担了 8+ 种职责：

| 职责 | 当前实现 |
|------|----------|
| 管理 LocalRouter 和 DistributedRouter | 直接创建和管理 |
| 创建和管理 WorkerPool | 直接创建每种业务类型 |
| 创建和管理 Kafka Consumer | 直接创建 |
| 创建和管理 DownstreamRouter | 在 connector 包 |
| 创建 AdminHandler、WsHandler | 直接创建 |
| 处理 WebSocket 升级 | 直接处理 |
| 管理连接生命周期 | 直接管理 |
| 连接计数管理 | 直接管理 |

**违反的原则**：
- 单一职责原则（SRP）
- 依赖反转原则（DIP）
- 难以单独测试某层
- 难以独立扩展某层

### 1.2 重构目标

- **传输层（Transport）**: 纯 I/O 操作，封装 TCP/WebSocket/gRPC
- **连接层（Connection）**: 协议编解码、双 Goroutine、Handler 调度
- **业务层（Worker）**: UpstreamSender、DownstreamRouter、WorkerPool、Kafka Consumer
- **协调层（Session）**: 依赖注入启动、HTTP 服务、连接计数、生命周期管理

---

## 2. 目标架构

### 2.1 整体架构图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Session (协调层)                                   │
│  · 依赖注入配置                                                                │
│  · 创建并注入各层组件                                                          │
│  · HTTP Admin / WebSocket 入口                                               │
│  · 连接计数管理                                                                │
│  · 启动/停止生命周期                                                           │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                        通过接口注入，而非直接创建
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                            Worker (业务层)                                   │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │  UpstreamManager (per BusinessType)                                  │  │
│  │  · UpstreamSender (Kafka/gRPC)                                       │  │
│  │  · UpstreamWorkerPool                                                │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │  DownstreamManager                                                   │  │
│  │  · DownstreamRouter (ConnID → Connection)                           │  │
│  │  · DownstreamWorkerPool                                              │  │
│  │  · KafkaConsumer (订阅下游 Topic)                                     │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                        下行消息写入 Connection.WriteCh
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          Connection (连接层)                                 │
│  · ReadLoop / WriteLoop (双 Goroutine)                                      │
│  · msglogic.Decode / Encode                                                │
│  · Handler 调度 (Auth / Heartbeat / Upstream)                               │
│  · 注册到 LocalRouter / DistributedRouter                                   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                        接收新连接
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          Transport (传输层)                                   │
│  · TCP / WebSocket / gRPC 封装                                             │
│  · 纯 I/O 操作，无业务逻辑                                                   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 各层职责边界

| 层 | 职责 | 不管什么 |
|----|------|----------|
| **Transport** | 纯 I/O：读原始字节、写协议数据、连接关闭 | 协议解析、业务逻辑 |
| **Connection** | 协议编解码、双 Goroutine、Handler 调度、注册路由 | Worker、消息队列 |
| **Worker** | UpstreamSender、DownstreamRouter、WorkerPool、Kafka Consumer | 连接细节、传输协议 |
| **Session** | 依赖注入启动、HTTP 服务、连接计数、生命周期管理 | 具体业务处理 |

---

## 3. 核心接口设计

### 3.1 Transport 层接口

```go
// Transport 传输层接口，纯 I/O 操作
type Transport interface {
    Read(ctx context.Context) (*gateway.ClientSignal, error)
    SetReadDeadline(t time.Time) error
    Write(ctx context.Context, data *gateway.ClientSignal) error
    Close() error
    RemoteAddr() string
}
```

**实现**:
- `TCPTransport`: 封装 net.Conn
- `WebSocketTransport`: 封装 gorilla/websocket.Conn
- `GRPTransport`: 封装 gRPC 流

### 3.2 Connection 层接口

```go
// ConnectionFactory 创建连接实例
type ConnectionFactory interface {
    CreateConnection(tp Transport) *Connection
}

// ConnectionAccessor 供 Handler 使用
type ConnectionAccessor interface {
    GetConnID() string
    SetUserInfo(userID, deviceID string)
    GetUserInfo() (userID, deviceID string)
    GetRemoteAddr() string
    GetWriteCh() chan *types.Message
    IsAuthed() bool
    SetAuthed(bool)
    RouterRegister(userID, deviceID string)
    RefreshRoute()
    SubmitUpstream(msg *types.Message) bool
}

// MsgHandler 消息处理接口
type MsgHandler interface {
    Handle(conn ConnectionAccessor, msg *types.Message) error
}

// HandlerRegistry 消息处理注册表
type HandlerRegistry interface {
    Register(msgType gateway.SignalType, handler MsgHandler)
    HandleMessage(conn ConnectionAccessor, msg *types.Message) error
}
```

### 3.3 Worker 层接口

```go
// UpstreamSubmitter 提交上行消息的接口
type UpstreamSubmitter interface {
    SubmitUpstream(msg *types.Message) bool
}

// DownstreamRouter 下行路由接口
type DownstreamRouter interface {
    Register(connID string, conn *Connection)
    Unregister(connID string)
    Route(msg *gateway.DownstreamKafkaMessage) error
}

// WorkerPoolInterface worker 池接口
type WorkerPoolInterface interface {
    Start()
    Stop()
    SubmitUpstream(job UpstreamJob) bool
    SubmitDownstream(job DownstreamJob) bool
}

// UpstreamSender 上行发送器接口
type UpstreamSender interface {
    Send(ctx context.Context, req *types.UpstreamRequest) error
    Kind() string
}

// WorkerFactory 创建 Worker 实例
type WorkerFactory interface {
    CreateWorkerPool(bizType gateway.BusinessType) WorkerPoolInterface
    CreateSender(bizType string) (UpstreamSender, error)
}

// DownstreamConsumerManager 下行消费者管理器接口
type DownstreamConsumerManager interface {
    Start(ctx context.Context) error
    Stop() error
}
```

### 3.4 Router 层接口

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

## 4. 目录结构

### 4.1 重构后目录结构

```
gateway/internal/
├── config/              # 配置加载
├── types/               # 内部类型定义 (Message, UpstreamRequest)
│
├── transport/           # 传输层 (新增)
│   ├── transport.go     # Transport 接口定义
│   ├── tcp.go           # TCP 实现
│   ├── websocket.go     # WebSocket 实现
│   └── grpc.go          # gRPC 实现
│
├── connection/          # 连接层 (重构)
│   ├── connection.go    # Connection 主结构
│   ├── factory.go       # ConnectionFactory
│   ├── handler.go       # MsgHandler 接口 + Registry
│   ├── auth.go          # AuthHandler
│   ├── heartbeat.go     # HeartbeatHandler
│   └── upstream.go      # UpstreamHandler
│
├── worker/              # Worker 层 (重构自 connector)
│   ├── pool.go          # WorkerPool (上行/下行分离)
│   ├── upstream/         # Upstream 相关
│   │   ├── sender.go     # SenderFactory + 接口
│   │   └── kafka.go     # KafkaSender 实现
│   ├── downstream/      # Downstream 相关
│   │   └── router.go    # DownstreamRouter
│   ├── consumer.go      # Kafka Consumer
│   └── manager.go       # WorkerManager (统一管理)
│
├── router/              # 路由层
│   ├── local.go         # LocalRouter
│   ├── distributed.go    # DistributedRouter
│   └── interfaces.go    # 路由接口定义
│
├── session/             # 协调层 (重构)
│   ├── session.go        # Session 主结构
│   ├── options.go        # 配置选项
│   └── accessor.go      # Session 访问接口
│
├── server/              # HTTP/入口 (重构自 cmd)
│   └── server.go
│
└── main.go
```

### 4.2 目录职责说明

| 目录 | 职责 |
|------|------|
| `transport/` | 纯 I/O 封装，对接 net.Conn / websocket.Conn |
| `connection/` | 连接生命周期、协议编解码、Handler 调度 |
| `worker/` | 消息队列生产者/消费者、Worker 池管理 |
| `router/` | LocalRouter（内存）、DistributedRouter（Redis） |
| `session/` | 依赖注入协调、HTTP 服务、连接计数 |
| `server/` | HTTP 路由注册、启动入口 |
| `config/` | 配置加载和验证 |
| `types/` | 内部通用类型定义 |

---

## 5. Session 协调层设计

### 5.1 Session 结构

```go
// Session 只负责协调，不处理具体业务
type Session struct {
    cfg         *config.Config

    // 路由层（直接持有）
    localRouter        *router.LocalRouter
    distributedRouter  *router.DistributedRouter

    // 注入的组件（通过选项注入）
    workerManager       WorkerManager
    transportManager    TransportManager
    connectionFactory   ConnectionFactory
    handlerRegistry     connection.HandlerRegistry

    // HTTP 服务
    httpServer  *http.Server

    // 连接计数
    connCount   uint32
    connCountMu sync/atomic
}
```

### 5.2 依赖注入方式

```go
// Option Session 配置选项
type Option func(s *Session)

func WithWorkerManager(wm WorkerManager) Option {
    return func(s *Session) { s.workerManager = wm }
}

func WithTransportManager(tm TransportManager) Option {
    return func(s *Session) { s.transportManager = tm }
}

// NewSession 创建 Session 实例
func NewSession(cfg *config.Config, opts ...Option) *Session {
    s := &Session{
        cfg: cfg,
        localRouter: router.NewLocalRouter(),
        distributedRouter: router.NewDistributedRouter(...),
    }

    // 应用选项
    for _, o := range opts {
        o(s)
    }

    // 默认实现（如果未注入）
    if s.workerManager == nil {
        s.workerManager = NewWorkerManager(...)
    }
    if s.transportManager == nil {
        s.transportManager = NewTransportManager(...)
    }
    if s.connectionFactory == nil {
        s.connectionFactory = NewConnectionFactory(...)
    }

    return s
}
```

### 5.3 Session 启动流程

```go
func (s *Session) Start() error {
    // 1. 启动 Worker Manager（会创建 Kafka Consumer）
    if err := s.workerManager.Start(s.ctx); err != nil {
        return err
    }

    // 2. 启动 Transport Manager（HTTP/Gin 服务）
    if err := s.transportManager.Start(s); err != nil {
        return err
    }

    // 3. 启动超时清理 Goroutine
    go s.localRouter.CleanTimeoutLoop()

    return nil
}
```

---

## 6. Worker 层设计

### 6.1 WorkerManager 结构

```go
// WorkerManager 统一管理所有业务类型的 Worker
type WorkerManager struct {
    cfg          *config.Config
    distRouter   router.DistributedRouterInterface
    localRouter  *router.LocalRouter

    pools       map[gateway.BusinessType]WorkerPoolInterface
    downstreamMgr DownstreamConsumerManager
}

func NewWorkerManager(cfg *config.Config, dr router.DistributedRouterInterface) *WorkerManager {
    wm := &WorkerManager{
        cfg:        cfg,
        distRouter: dr,
        pools:      make(map[gateway.BusinessType]WorkerPoolInterface),
    }

    // 为每种业务类型创建 WorkerPool
    for bizType := range cfg.Upstream.Kafka.BusinessTopics {
        // 创建 DownstreamRouter（持有到 Connection 的映射）
        dr := downstream.NewDownstreamRouter()

        // 创建 UpstreamSender
        senderFactory := upstream.NewSenderFactory(cfg.Upstream)
        sender, _ := senderFactory.CreateSender(bizType)

        // 创建 WorkerPool
        pool := worker.NewWorkerPool(cfg, bizType, sender, dr)
        wm.pools[bizType] = pool
    }

    // 创建 Kafka Consumer Manager
    wm.downstreamMgr = NewDownstreamConsumerManager(cfg, wm.pools)

    return wm
}
```

### 6.2 DownstreamRouter 结构

```go
// DownstreamRouter 下行路由：根据 ConnID 找到 Connection
type DownstreamRouter struct {
    connIDMap map[string]*connection.Connection
    mux       sync.RWMutex
}

func (dr *DownstreamRouter) Register(connID string, conn *connection.Connection) {
    dr.mux.Lock()
    defer dr.mux.Unlock()
    dr.connIDMap[connID] = conn
}

func (dr *DownstreamRouter) Unregister(connID string) {
    dr.mux.Lock()
    defer dr.mux.Unlock()
    delete(dr.connIDMap, connID)
}

func (dr *DownstreamRouter) Route(msg *gateway.DownstreamKafkaMessage) error {
    dr.mux.RLock()
    conn, ok := dr.connIDMap[msg.ConnId]
    dr.mux.RUnlock()

    if !ok {
        return ErrConnectionNotFound
    }

    // 写入 Connection 的 WriteCh
    protoMsg := &types.Message{
        MsgID: msg.CorrelationId,
        Body:  msg.Payload,
    }

    select {
    case conn.WriteCh <- protoMsg:
        return nil
    default:
        return ErrWriteChannelFull
    }
}
```

---

## 7. Connection 层设计

### 7.1 Connection 结构

```go
// Connection 连接实例，管理连接生命周期
type Connection struct {
    // 传输层（纯 I/O）
    tp transport.Transport

    // 生命周期控制
    ctx    context.Context
    cancel context.CancelFunc

    // 写通道：唯一下行消息入口，串行化写操作
    WriteCh chan *types.Message

    // 连接唯一标识
    connID string

    // 用户信息（鉴权后绑定）
    userID   string
    deviceID string

    // 状态
    isAuthed    bool
    lastActive  time.Time
    mux         sync.RWMutex

    // 路由接口（注入）
    localRouter  router.LocalRouterInterface
    distRouter   router.DistributedRouterInterface

    // 上行提交器（注入，来自 Worker 层）
    upstreamSubmitter worker.UpstreamSubmitter
}
```

### 7.2 ConnectionFactory

```go
// ConnectionFactory 创建 Connection 实例
type ConnectionFactory struct {
    localRouter       router.LocalRouterInterface
    distRouter        router.DistributedRouterInterface
    upstreamSubmitter worker.UpstreamSubmitter
}

func NewConnectionFactory(lr router.LocalRouterInterface, dr router.DistributedRouterInterface) *ConnectionFactory {
    return &ConnectionFactory{
        localRouter: lr,
        distRouter:  dr,
    }
}

func (f *ConnectionFactory) CreateConnection(tp transport.Transport) *Connection {
    conn := NewConnection(tp)
    conn.SetRouters(f.localRouter, f.distRouter)
    conn.SetUpstreamSubmitter(f.upstreamSubmitter)
    return conn
}
```

---

## 8. 重构实施步骤

### 8.1 阶段划分

**阶段一：创建新包结构**
1. 创建 `transport/` 包，定义接口和实现
2. 创建 `worker/` 包，移动并重构 `connector/` 相关代码
3. 创建 `router/interfaces.go`，统一路由接口定义

**阶段二：重构 Session**
1. 重构 `session/` 包，采用依赖注入
2. 将 Handler 创建逻辑移入 Session
3. 简化 Session 职责

**阶段三：连接层重构**
1. 完善 `connection/` 包结构
2. 解耦 Connection 和具体 Worker 实现

**阶段四：清理**
1. 删除旧 `connector/` 包
2. 更新 import 路径
3. 运行测试验证

### 8.2 风险控制

- **接口稳定性**：接口定义后不应轻易改变
- **向后兼容**：先增加新接口，再逐步迁移
- **测试覆盖**：每层独立测试后再集成

---

## 9. 验收标准

1. **Session 瘦身后**：Session 代码行数减少 50%+
2. **接口清晰**：各层通过接口交互，无直接循环依赖
3. **可测试性**：每层可以独立单元测试
4. **功能不变**：重构后行为与重构前完全一致
5. **编译通过**：无编译错误

---

## 10. 附录

### 10.1 接口依赖关系图

```
Session
  ├─► LocalRouterInterface
  ├─► DistributedRouterInterface
  ├─► WorkerManager
  ├─► TransportManager
  └─► ConnectionFactory

WorkerManager
  ├─► WorkerPoolInterface (per bizType)
  ├─► DownstreamConsumerManager
  └─► DistributedRouterInterface

WorkerPool
  ├─► UpstreamSender
  └─► DownstreamRouter

Connection
  ├─► Transport (注入)
  ├─► LocalRouterInterface (注入)
  ├─► DistributedRouterInterface (注入)
  └─► UpstreamSubmitter (注入)
```

### 10.2 待定项

- [ ] gRPC 传输层实现细节
- [ ] 连接池复用策略
- [ ] 监控指标集成方式

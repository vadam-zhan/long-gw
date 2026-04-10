# Gateway 分层重构设计方案

**日期**: 2026-04-10
**状态**: 设计完成，待实施
**目标**: 分离传输层、连接层、业务层，让 Session 只负责协调
**版本**: v2.0（详细 Worker 层设计）

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

### 6.0 消息流概览

```
┌────────────────────────────────────────────────────────────────────────────────┐
│                                  上行消息流                                       │
│                                                                                │
│   Client → Transport → Connection.ReadLoop → Handler → UpstreamHandler         │
│                                                    │                           │
│                                                    ▼                           │
│                                           UpstreamSubmitter                     │
│                                                    │                           │
│                                                    ▼                           │
│                                            WorkerPool.UpstreamCh                │
│                                                    │                           │
│                                                    ▼                           │
│                                          UpstreamWorker (goroutine)             │
│                                                    │                           │
│                                                    ▼                           │
│                                          UpstreamSender.Send()                  │
│                                                    │                           │
│                                                    ▼                           │
│                                         Kafka (upstream topic)                  │
│                                                    │                           │
│                                                    ▼                           │
│                                         Business Service                        │
└────────────────────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────────────────────┐
│                                  下行消息流                                       │
│                                                                                │
│   Business Service → Kafka (downstream topic)                                 │
│                            │                                                   │
│                            ▼                                                   │
│                 KafkaConsumer.FetchMessage()                                   │
│                            │                                                   │
│                            ▼                                                   │
│               DownstreamConsumerManager (分发到对应 bizType)                   │
│                            │                                                   │
│                            ▼                                                   │
│                     WorkerPool.DownstreamCh                                    │
│                            │                                                   │
│                            ▼                                                   │
│                 DownstreamWorker (goroutine)                                   │
│                            │                                                   │
│                            ▼                                                   │
│                      DownstreamRouter.Route()                                  │
│                            │                                                   │
│                            ▼                                                   │
│              Connection.WriteCh ←────────────────────────────                  │
│                            │                                                   │
│                            ▼                                                   │
│                 Connection.WriteLoop → Transport.Write                          │
│                            │                                                   │
│                            ▼                                                   │
│                            Client                                              │
└────────────────────────────────────────────────────────────────────────────────┘
```

### 6.1 核心数据结构

```go
// UpstreamJob 上行任务（从 Connection 到 Kafka）
type UpstreamJob struct {
    Msg      *types.Message       // 解析后的内部消息
    ConnID   string               // 连接 ID，用于追踪
    UserID   string               // 用户 ID
    DeviceID string               // 设备 ID
    BizType  gateway.BusinessType // 业务类型
    Ctx      context.Context      // 上下文，用于超时控制
}

// DownstreamJob 下行任务（从 Kafka 到 Connection）
type DownstreamJob struct {
    Msg         *gateway.DownstreamKafkaMessage // Kafka 消息
    BizType     gateway.BusinessType             // 业务类型
    Offset      int64                            // Kafka offset，用于提交
}
```

### 6.2 WorkerManager 结构

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

### 6.2 WorkerPool 详细设计

```go
// WorkerPool 核心结构
type WorkerPool struct {
    bizType gateway.BusinessType

    // 上行相关
    upstreamCh     chan UpstreamJob    // 上行任务通道
    upstreamSender UpstreamSender      // 发送器（Kafka/gRPC）

    // 下行相关
    downstreamCh       chan DownstreamJob // 下行任务通道
    downstreamRouter    DownstreamRouter   // 下行路由

    // 生命周期
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

// NewWorkerPool 创建 WorkerPool
func NewWorkerPool(
    cfg *config.Config,
    bizType gateway.BusinessType,
    sender UpstreamSender,
    router DownstreamRouter,
) *WorkerPool {
    ctx, cancel := context.WithCancel(context.Background())

    pool := &WorkerPool{
        bizType:          bizType,
        upstreamCh:       make(chan UpstreamJob, cfg.Gateway.UpstreamWorkerNum*10),
        downstreamCh:     make(chan DownstreamJob, cfg.Gateway.DownstreamWorkerNum*10),
        upstreamSender:   sender,
        downstreamRouter: router,
        ctx:              ctx,
        cancel:           cancel,
    }

    // 启动 Worker
    pool.start()

    return pool
}
```

### 6.3 Worker 启动流程

```go
func (p *WorkerPool) start() {
    // 上行 Worker：数量 = channel容量 / 10
    upstreamWorkerNum := cap(p.upstreamCh) / 10
    for i := 0; i < upstreamWorkerNum; i++ {
        p.wg.Add(1)
        go p.upstreamWorker(i)
    }

    // 下行 Worker：数量 = channel容量 / 10
    downstreamWorkerNum := cap(p.downstreamCh) / 10
    for i := 0; i < downstreamWorkerNum; i++ {
        p.wg.Add(1)
        go p.downstreamWorker(i)
    }
}

// upstreamWorker 处理上行任务
func (p *WorkerPool) upstreamWorker(id int) {
    defer p.wg.Done()

    for {
        select {
        case <-p.ctx.Done():
            return
        case job, ok := <-p.upstreamCh:
            if !ok {
                return
            }
            p.processUpstream(job)
        }
    }
}

// downstreamWorker 处理下行任务
func (p *WorkerPool) downstreamWorker(id int) {
    defer p.wg.Done()

    for {
        select {
        case <-p.ctx.Done():
            return
        case job, ok := <-p.downstreamCh:
            if !ok {
                return
            }
            p.processDownstream(job)
        }
    }
}
```

### 6.4 上行消息处理流程

```
Connection.ReadLoop()
    │
    │ 解析消息
    ▼
msglogic.Decode(clientSignal) → *types.Message
    │
    │ Handler 调度
    ▼
UpstreamHandler.Handle()
    │
    │ 调用 SubmitUpstream()
    ▼
WorkerPool.SubmitUpstream(job)
    │ 非阻塞 select
    ▼
upstreamCh ← job  (写入 channel)
    │
    │ upstreamWorker 消费
    ▼
processUpstream(job)
    │
    │ 调用 UpstreamSender
    ▼
sender.Send(ctx, upstreamRequest)
    │
    │ 发送到 Kafka
    ▼
Kafka (upstream topic)
```

**详细代码**：

```go
// SubmitUpstream 提交上行任务（非阻塞）
func (p *WorkerPool) SubmitUpstream(job UpstreamJob) bool {
    select {
    case p.upstreamCh <- job:
        return true
    default:
        // channel 满，拒绝任务
        return false
    }
}

// processUpstream 处理上行任务
func (p *WorkerPool) processUpstream(job UpstreamJob) {
    defer func() {
        if r := recover(); r != nil {
            logger.Error("processUpstream panic",
                zap.Any("error", r),
                zap.String("conn_id", job.ConnID))
        }
    }()

    // 填充用户信息
    req := &types.UpstreamRequest{
        ConnID:    job.ConnID,
        UserID:    job.UserID,
        DeviceID:  job.DeviceID,
        BizType:   job.BizType,
        Msg:       job.Msg,
        Timestamp: time.Now().UnixMilli(),
    }

    // 发送失败时记录日志（不阻塞）
    if err := p.upstreamSender.Send(job.Ctx, req); err != nil {
        logger.Error("upstream send failed",
            zap.String("conn_id", job.ConnID),
            zap.String("biz_type", job.BizType.String()),
            zap.Error(err))
    }
}
```

### 6.5 下行消息处理流程

```
Business Service
    │
    │ 发送下行消息
    ▼
Kafka (downstream topic: gateway-{biz}-downstream)
    │
    │ KafkaConsumer.FetchMessage()
    ▼
DownstreamConsumerManager.Dispatch()
    │
    │ 根据 msg.BusinessType 分发
    ▼
WorkerPool.SubmitDownstream(job)
    │ 非阻塞 select
    ▼
downstreamCh ← job  (写入 channel)
    │
    │ downstreamWorker 消费
    ▼
processDownstream(job)
    │
    │ 调用 DownstreamRouter.Route()
    ▼
DownstreamRouter.Route(msg)
    │
    │ 查找 Connection
    ▼
Connection.WriteCh ← protoMsg
    │
    │ Connection.WriteLoop 消费
    ▼
msglogic.Encode() → ClientSignal
    │
    │ Transport.Write()
    ▼
Client
```

**详细代码**：

```go
// DownstreamConsumerManager 下行消费者管理器
type DownstreamConsumerManager struct {
    cfg     *config.KafkaConfig
    pools   map[gateway.BusinessType]WorkerPoolInterface
    readers map[string]*kafka.Reader // per topic
    wg      sync.WaitGroup
}

func (m *DownstreamConsumerManager) Start(ctx context.Context) error {
    topics := m.getDownstreamTopics()

    for _, topic := range topics {
        m.wg.Add(1)
        go m.consumeTopic(ctx, topic)
    }

    return nil
}

func (m *DownstreamConsumerManager) consumeTopic(ctx context.Context, topic string) {
    defer m.wg.Done()

    reader := kafka.NewReader(kafka.ReaderConfig{
        Brokers:  m.cfg.Brokers,
        GroupID:  "gateway-downstream-group",
        Topic:    topic,
        MinBytes: 1,
        MaxBytes: 10e6,
    })

    for {
        select {
        case <-ctx.Done():
            return
        default:
            msg, err := reader.FetchMessage(ctx)
            if err != nil {
                continue
            }

            // 反序列化
            downstreamMsg := &gateway.DownstreamKafkaMessage{}
            if err := proto.Unmarshal(msg.Value, downstreamMsg); err != nil {
                reader.CommitMessages(ctx, msg) // 跳过毒消息
                continue
            }

            // 提交到对应 bizType 的 WorkerPool
            pool, ok := m.pools[downstreamMsg.BusinessType]
            if !ok {
                logger.Warn("no pool for biz type",
                    zap.String("biz_type", downstreamMsg.BusinessType.String()))
                reader.CommitMessages(ctx, msg)
                continue
            }

            job := DownstreamJob{
                Msg:    downstreamMsg,
                BizType: downstreamMsg.BusinessType,
                Offset:  msg.Offset,
            }

            if !pool.SubmitDownstream(job) {
                // 拒绝时不提交，消息会被重新投递
                continue
            }

            // 成功后提交 offset
            reader.CommitMessages(ctx, msg)
        }
    }
}

// SubmitDownstream 提交下行任务（非阻塞）
func (p *WorkerPool) SubmitDownstream(job DownstreamJob) bool {
    select {
    case p.downstreamCh <- job:
        return true
    default:
        return false
    }
}

// processDownstream 处理下行任务
func (p *WorkerPool) processDownstream(job DownstreamJob) {
    defer func() {
        if r := recover(); r != nil {
            logger.Error("processDownstream panic",
                zap.Any("error", r))
        }
    }()

    // 路由到对应 Connection
    if err := p.downstreamRouter.Route(job.Msg); err != nil {
        logger.Debug("downstream route failed",
            zap.String("conn_id", job.Msg.ConnId),
            zap.Error(err))
    }
}
```

### 6.6 DownstreamRouter 结构

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

// Route 将下行消息路由到 Connection 的 WriteCh
func (dr *DownstreamRouter) Route(msg *gateway.DownstreamKafkaMessage) error {
    dr.mux.RLock()
    conn, ok := dr.connIDMap[msg.ConnId]
    dr.mux.RUnlock()

    if !ok {
        return ErrConnectionNotFound
    }

    // 转换为内部消息格式
    protoMsg := &types.Message{
        MsgID: msg.CorrelationId,
        Body:  msg.Payload,
    }

    // 写入 Connection 的写通道（非阻塞）
    select {
    case conn.WriteCh <- protoMsg:
        return nil
    default:
        return ErrWriteChannelFull
    }
}
```

### 6.7 Connection 如何向 Worker 提交上行消息

Connection 持有 `UpstreamSubmitter` 接口，不直接持有 WorkerPool：

```go
// Connection 结构（简化）
type Connection struct {
    tp               transport.Transport
    upstreamSubmitter worker.UpstreamSubmitter  // 注入的接口
    // ...
}

// SubmitUpstream 提交上行消息
func (c *Connection) SubmitUpstream(msg *types.Message) bool {
    if c.upstreamSubmitter == nil {
        return false
    }

    job := UpstreamJob{
        Msg:      msg,
        ConnID:   c.connID,
        UserID:   c.userID,
        DeviceID: c.deviceID,
        BizType:  msg.BizType,
        Ctx:      c.ctx,
    }

    return c.upstreamSubmitter.SubmitUpstream(job)
}
```

**UpstreamHandler.Handle() 调用链**：

```go
// UpstreamHandler 处理上行业务消息
func (h *UpstreamHandler) Handle(conn ConnectionAccessor, msg *types.Message) error {
    // 刷新分布式路由 TTL
    if conn.IsAuthed() {
        conn.RefreshRoute()
    }

    // 提交到 Worker 池
    return conn.SubmitUpstream(msg)
}
```

### 6.8 Connection 如何注册/注销到 DownstreamRouter

当 Connection 鉴权成功时，需要注册到 DownstreamRouter：

```go
// Connection.authHandler.Handle() 鉴权成功后调用
func (h *AuthHandler) Handle(conn ConnectionAccessor, msg *types.Message) error {
    // ... 鉴权逻辑 ...

    conn.SetUserInfo(userID, deviceID)
    conn.SetAuthed(true)
    conn.RouterRegister(userID, deviceID)

    // 注意：DownstreamRouter 注册由 Session 协调层负责
    // Session 在创建 Connection 后，调用 downstreamRouter.Register()
    return nil
}
```

DownstreamRouter 的注册/注销由 Session 协调层在 Connection 创建和关闭时调用：

```go
// Session.HandleConnection()
func (s *Session) HandleConnection(tp transport.Transport) {
    // 创建 Connection
    conn := s.connectionFactory.CreateConnection(tp)

    // 注册到 DownstreamRouter（Worker 层）
    if s.workerManager != nil {
        s.workerManager.RegisterConnection(conn.GetConnID(), conn)
    }

    // 启动读写循环...

    // 连接关闭时注销
    defer func() {
        if s.workerManager != nil {
            s.workerManager.UnregisterConnection(conn.GetConnID())
        }
    }()
}
```

### 6.9 完整调用时序图

```
Client                    Gateway                          Kafka                   Business
 │                          │                               │                         │
 │  1. TCP/WebSocket 连接    │                               │                         │
 │─────────────────────────>│                               │                         │
 │                          │                               │                         │
 │  2. 发送 AuthRequest     │                               │                         │
 │─────────────────────────>│                               │                         │
 │                          │  3. ReadLoop 读取              │                         │
 │                          │  4. Decode → Message           │                         │
 │                          │  5. Handler 调度               │                         │
 │                          │  6. AuthHandler.Handle()      │                         │
 │                          │  7. 调用 Auth Service         │                         │
 │                          │───────────────────────────────>│                         │
 │                          │                               │                         │
 │                          │  8. 鉴权成功                   │                         │
 │                          │<──────────────────────────────│                         │
 │                          │                               │                         │
 │                          │  9. conn.RouterRegister()     │                         │
 │                          │     (LocalRouter + DistRouter)│                         │
 │                          │                               │                         │
 │                          │  10. WorkerManager.Register() │                         │
 │                          │     (DownstreamRouter)         │                         │
 │                          │                               │                         │
 │  11. AuthResponse        │                               │                         │
 │<─────────────────────────│                               │                         │
 │                          │                               │                         │
 │  12. 发送 BusinessUp     │                               │                         │
 │─────────────────────────>│                               │                         │
 │                          │  13. ReadLoop 读取             │                         │
 │                          │  14. UpstreamHandler.Handle() │                         │
 │                          │  15. conn.SubmitUpstream()    │                         │
 │                          │  16. upstreamCh ← job        │                         │
 │                          │                               │                         │
 │                          │  17. upstreamWorker.Send()   │                         │
 │                          │────────────────────────────────────────────────>│
 │                          │                               │  18. 消费处理            │
 │                          │                               │                         │
 │  19. (等待响应)           │                               │                         │
 │<─────────────────────────│                               │                         │
 │                          │                               │                         │
 │                          │                               │ 20. 发送 DownstreamMsg  │
 │                          │                               │<────────────────────────│
 │                          │                               │                         │
 │                          │  21. Kafka.FetchMessage()    │                         │
 │                          │<──────────────────────────────│                         │
 │                          │                               │                         │
 │                          │  22. downstreamCh ← job      │                         │
 │                          │                               │                         │
 │                          │  23. downstreamWorker.Route() │                         │
 │                          │  24. WriteCh ← protoMsg      │                         │
 │                          │                               │                         │
 │                          │  25. WriteLoop 消费           │                         │
 │                          │  26. Encode → ClientSignal   │                         │
 │                          │  27. Transport.Write()       │                         │
 │                          │                               │                         │
 │  28. BusinessDown        │                               │                         │
 │<─────────────────────────│                               │                         │
```

### 6.10 Session 与 WorkerManager 的交互

```go
// WorkerManager 接口
type WorkerManager interface {
    Start(ctx context.Context) error
    Stop()

    // Connection 注册/注销（由 Session 调用）
    RegisterConnection(connID string, conn *connection.Connection)
    UnregisterConnection(connID string)

    // 获取 UpstreamSubmitter（注入给 ConnectionFactory）
    GetUpstreamSubmitter() UpstreamSubmitter
}

// WorkerManager 实现
type workerManager struct {
    cfg           *config.Config
    pools         map[gateway.BusinessType]*WorkerPool
    downstreamMgr *DownstreamConsumerManager
    downstreamRouter *DownstreamRouter  // 所有 pool 共享同一个 router
}

func (m *workerManager) RegisterConnection(connID string, conn *connection.Connection) {
    m.downstreamRouter.Register(connID, conn)
}

func (m *workerManager) UnregisterConnection(connID string) {
    m.downstreamRouter.Unregister(connID)
}

func (m *workerManager) GetUpstreamSubmitter() UpstreamSubmitter {
    // 返回一个多路复用的 submitter，根据 bizType 分发到不同 pool
    return &multiBizTypeSubmitter{pools: m.pools}
}

// multiBizTypeSubmitter 实现 UpstreamSubmitter 接口
type multiBizTypeSubmitter struct {
    pools map[gateway.BusinessType]*WorkerPool
}

func (s *multiBizTypeSubmitter) SubmitUpstream(msg *types.Message) bool {
    pool, ok := s.pools[msg.BizType]
    if !ok {
        return false
    }

    job := UpstreamJob{
        Msg:      msg,
        ConnID:   msg.ConnID,
        UserID:   msg.UserID,
        DeviceID: msg.DeviceID,
        BizType:  msg.BizType,
        Ctx:      context.Background(),
    }

    return pool.SubmitUpstream(job)
}
```

### 6.11 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| UpstreamSubmitter 接口 | 注入到 Connection | Connection 不需要知道 WorkerPool 的存在 |
| DownstreamRouter 共享 | 所有 bizType 共享 | 一个 ConnID 只属于一个 Connection |
| Job 非阻塞提交 | select + default | 防止慢 worker 阻塞快速路径 |
| Kafka offset 提交时机 | WorkerPool 接受后提交 | 保证不丢消息 |
| Channel 容量 | workerNum * 10 | 提供背压能力又不耗尽内存 |

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

### 10.3 Worker 层组件清单

| 组件 | 文件 | 职责 |
|------|------|------|
| `WorkerPool` | `worker/pool.go` | 上行/下行 Worker 池管理 |
| `UpstreamJob` | `worker/pool.go` | 上行任务结构 |
| `DownstreamJob` | `worker/pool.go` | 下行任务结构 |
| `UpstreamSender` | `worker/upstream/sender.go` | 上行发送接口 |
| `KafkaSender` | `worker/upstream/kafka.go` | Kafka 上行发送实现 |
| `GRPCSender` | `worker/upstream/grpc.go` | gRPC 上行发送实现 |
| `DownstreamRouter` | `worker/downstream/router.go` | 下行路由（ConnID → Connection） |
| `DownstreamConsumerManager` | `worker/consumer.go` | Kafka 下行消费者管理 |
| `WorkerManager` | `worker/manager.go` | 统一管理所有 Worker 组件 |
| `multiBizTypeSubmitter` | `worker/manager.go` | 多业务类型上行提交器 |

### 10.4 类型定义位置

| 类型 | 位置 | 说明 |
|------|------|------|
| `types.Message` | `types/message.go` | 内部消息表示 |
| `types.UpstreamRequest` | `types/upstream.go` | 上行请求结构 |
| `UpstreamJob` | `worker/pool.go` | 上行任务（channel 传输） |
| `DownstreamJob` | `worker/pool.go` | 下行任务（channel 传输） |
| `gateway.ClientSignal` | `common-protocol/v1/` | Protobuf 协议定义 |
| `gateway.DownstreamKafkaMessage` | `common-protocol/v1/` | Kafka 下行消息格式 |

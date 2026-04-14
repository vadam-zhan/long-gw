package connector

import (
	"context"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connector/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/consts"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/logic/msglogic"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// LocalRouterInterface 本地路由接口
type LocalRouterInterface interface {
	Register(userID, deviceID string, conn ConnectionInterface)
	UnRegister(conn ConnectionInterface)
}

// DistRouterInterface 分布式路由接口
type DistRouterInterface interface {
	RegisterUser(ctx context.Context, userID, deviceID string) error
	RefreshRoute(ctx context.Context, userID, deviceID string) error
	UnregisterUser(ctx context.Context, userID, deviceID string) error
}

// ConnectionInterface 连接接口（供路由层使用）
type ConnectionInterface interface {
	GetConnID() string
	GetUserInfo() (userID, deviceID string)
	Close()
	IsTimeout() bool
}

// Connection 连接实例，管理连接生命周期
// 负责：协议编解码、双goroutine模型、路由注册、worker池交互
type Connection struct {
	// 传输层（纯I/O）
	tp transport.Transport

	// 生命周期控制
	ctx    context.Context
	cancel context.CancelFunc

	// 写通道：唯一下行消息入口，串行化写操作
	WriteCh chan *types.Message

	// 连接唯一标识（用于Kafka路由）
	connID string

	// 用户信息（鉴权后绑定）
	userID   string
	deviceID string

	state gateway.ConnState
	// 活跃时间
	lastActive time.Time
	mux        sync.RWMutex

	// 是否已鉴权
	isAuthed bool

	// 路由接口
	localRouter LocalRouterInterface
	distRouter  DistRouterInterface

	// Worker池（用于上行消息转发）
	workerPools map[gateway.BusinessType]WorkerPoolInterface

	// refreshCtx 用于刷新分布式路由的复用上下文
	refreshCtx    context.Context
	refreshCancel context.CancelFunc
}

// 确保Connection实现了ConnectionInterface
var _ ConnectionInterface = (*Connection)(nil)

// NewConnection 创建连接实例
func NewConnection(tp transport.Transport) *Connection {
	ctx, cancel := context.WithCancel(context.Background())
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 3*time.Second)
	return &Connection{
		tp:            tp,
		ctx:           ctx,
		cancel:        cancel,
		WriteCh:       make(chan *types.Message, consts.WriteChannelSize),
		connID:        uuid.NewString(),
		lastActive:    time.Now(),
		refreshCtx:    refreshCtx,
		refreshCancel: refreshCancel,
	}
}

// SetUserInfo 设置用户信息（鉴权成功后调用）
func (c *Connection) SetUserInfo(userID, deviceID string) {
	c.userID = userID
	c.deviceID = deviceID
}

// SetWorkerPool 设置worker池
func (c *Connection) SetWorkerPool(pool map[gateway.BusinessType]WorkerPoolInterface) {
	c.workerPools = pool
}

// ReadLoop 读取并处理消息
func (c *Connection) ReadLoop() {
	defer func() {
		// if err := recover(); err != nil {
		// 	logger.Error("readLoop panic",
		// 		zap.Any("error", err),
		// 		zap.String("remote", c.tp.RemoteAddr()))
		// }
		c.cancel()
	}()

	logger.Debug("readLoop started",
		zap.String("remote", c.tp.RemoteAddr()))

	// 鉴权超时控制
	authTimer := time.AfterFunc(consts.AuthTimeout, func() {
		if !c.isAuthed {
			logger.Warn("auth timeout, closing connection",
				zap.String("remote", c.tp.RemoteAddr()))
			c.cancel()
		}
	})
	defer authTimer.Stop()

	for {
		select {
		case <-c.ctx.Done():
			logger.Debug("readLoop exit",
				zap.String("remote", c.tp.RemoteAddr()))
			return

		default:
			// 设置读取超时
			if err := c.tp.SetReadDeadline(time.Now().Add(consts.HeartbeatTimeout)); err != nil {
				logger.Error("set read deadline failed",
					zap.Error(err),
					zap.String("remote", c.tp.RemoteAddr()))
				return
			}

			// 读取原始数据
			data, err := c.tp.Read(c.ctx)
			if err != nil {
				logger.Error("read message failed",
					zap.Error(err),
					zap.String("remote", c.tp.RemoteAddr()))
				return
			}

			// 更新活跃时间
			c.mux.Lock()
			c.lastActive = time.Now()
			c.mux.Unlock()

			// 解析消息
			msg, err := msglogic.Decode(data)
			if err != nil {
				logger.Warn("decode message failed",
					zap.Error(err),
					zap.String("remote", c.tp.RemoteAddr()))
				continue
			}

			// 使用 handler 处理消息
			logger.Info("readLoop received message",
				zap.Any("msgID", msg),
				zap.String("remote", c.tp.RemoteAddr()))
			if err := connection.GlobalHandlerRegistry.HandleMessage(c.ctx, c, msg); err != nil {
				logger.Error("handleMessage message failed",
					zap.Error(err),
					zap.String("remote", c.tp.RemoteAddr()))
			}
		}
	}
}

// WriteLoop 处理下行消息写入
func (c *Connection) WriteLoop() {
	defer func() {
		if err := recover(); err != nil {
			logger.Error("writeLoop panic",
				zap.Any("error", err),
				zap.String("remote", c.tp.RemoteAddr()))
		}
		c.cancel()
	}()

	logger.Debug("writeLoop started",
		zap.String("remote", c.tp.RemoteAddr()))

	for {
		select {
		case <-c.ctx.Done():
			logger.Debug("writeLoop exit",
				zap.String("remote", c.tp.RemoteAddr()))
			return

		case msg, ok := <-c.WriteCh:
			if !ok {
				return
			}
			logger.Info("writeLoop write msg", zap.Any("msg", msg))
			data, err := msglogic.Encode(msg)
			if err != nil {
				logger.Warn("encode message failed",
					zap.Error(err),
					zap.String("remote", c.tp.RemoteAddr()))
				continue
			}
			if err := c.tp.Write(c.ctx, data); err != nil {
				logger.Warn("write message failed",
					zap.Error(err),
					zap.String("remote", c.tp.RemoteAddr()))
				return
			}
		}
	}
}

// GetUserInfo 获取用户信息
func (c *Connection) GetUserInfo() (string, string) {
	return c.userID, c.deviceID
}

// GetConnID 获取连接ID
func (c *Connection) GetConnID() string {
	return c.connID
}

// IsTimeout 检查连接是否超时
func (c *Connection) IsTimeout() bool {
	c.mux.Lock()
	defer c.mux.Unlock()
	return time.Since(c.lastActive) > consts.HeartbeatTimeout
}

// Close 关闭连接
func (c *Connection) Close() {
	c.cancel()
	c.refreshCancel()
	c.tp.Close()
	close(c.WriteCh)

	if c.userID != "" && c.deviceID != "" {
		if c.localRouter != nil {
			c.localRouter.UnRegister(c)
		}
		if c.distRouter != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			c.distRouter.UnregisterUser(ctx, c.userID, c.deviceID)
			cancel()
		}
	}
}

// SetRouters 设置路由器
func (c *Connection) SetRouters(local LocalRouterInterface, dist DistRouterInterface) {
	c.localRouter = local
	c.distRouter = dist
}

// ===============================
// ConnectionAccessor 接口实现
// ===============================

// GetRemoteAddr 获取远端地址
func (c *Connection) GetRemoteAddr() string {
	return c.tp.RemoteAddr()
}

// GetWriteCh 获取写通道
func (c *Connection) GetWriteCh() chan *types.Message {
	return c.WriteCh
}

// Context 获取上下文
func (c *Connection) Context() context.Context {
	return c.ctx
}

func (c *Connection) IsAuthed() bool {
	return c.isAuthed
}

func (c *Connection) SetAuthed(authed bool) {
	c.isAuthed = authed
}

func (c *Connection) RouterRegister(userID, deviceID string) {
	c.localRouter.Register(userID, deviceID, c)
	c.distRouter.RegisterUser(c.ctx, userID, deviceID)
}

// RefreshRoute 刷新分布式路由
func (c *Connection) RefreshRoute() {
	if c.isAuthed && c.distRouter != nil {
		c.refreshCancel()
		c.refreshCtx, c.refreshCancel = context.WithTimeout(context.Background(), 3*time.Second)
		_ = c.distRouter.RefreshRoute(c.refreshCtx, c.userID, c.deviceID)
	}
}

// SubmitUpstream 提交上行业务消息到 worker 池
func (c *Connection) SubmitUpstream(ctx context.Context, msg *types.Message) connection.SubmitResult {
	if c.workerPools == nil {
		logger.Debug("worker pool not set, skipping upstream message",
			zap.String("msg_id", msg.RequestID))
		return connection.SubmitResult{Accepted: false, Reason: "no_worker_pool"}
	}

	// 从 Payload 提取 BizType
	var bizType gateway.BusinessType
	if bp, ok := msg.Payload.(*types.BusinessPayload); ok {
		bizType = bp.BizType.Proto()
	}

	job := UpstreamJob{
		Msg:  msg,
		Ctx:  ctx,
		Conn: c,
	}
	accepted := c.workerPools[bizType].SubmitUpstream(job)
	return connection.SubmitResult{Accepted: accepted, Reason: "ok"}
}

package connection

import (
	"context"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/consts"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/logic/msglogic"
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Connection 连接实例，管理连接生命周期
// 负责：协议编解码、双goroutine模型、路由注册、上行提交
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

	// 活跃时间
	lastActive time.Time
	mux        sync.RWMutex

	// 是否已鉴权
	isAuthed bool

	// 路由接口
	localRouter router.LocalRouterInterface
	distRouter  router.DistributedRouterInterface

	// 上行发送器（由 Session 注入）
	upstreamSender UpstreamSender

	// refreshCtx 用于刷新分布式路由的复用上下文
	refreshCtx    context.Context
	refreshCancel context.CancelFunc
}

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

// SetRouters 设置路由器
func (c *Connection) SetRouters(local router.LocalRouterInterface, dist router.DistributedRouterInterface) {
	c.localRouter = local
	c.distRouter = dist
}

// SetUpstreamSender 设置上行发送器
func (c *Connection) SetUpstreamSender(sender UpstreamSender) {
	c.upstreamSender = sender
}

// ReadLoop 读取并处理消息
func (c *Connection) ReadLoop() {
	defer func() {
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
			if err := GlobalHandlerRegistry.HandleMessage(c.ctx, c, msg); err != nil {
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

// Write 实现 worker.ConnectionWriter 接口
func (c *Connection) Write(msg *types.Message) bool {
	select {
	case c.WriteCh <- msg:
		return true
	default:
		return false
	}
}

// Context 获取上下文
func (c *Connection) Context() context.Context {
	return c.ctx
}

// IsAuthed 检查是否已鉴权
func (c *Connection) IsAuthed() bool {
	return c.isAuthed
}

// SetAuthed 设置鉴权状态
func (c *Connection) SetAuthed(authed bool) {
	c.isAuthed = authed
}

// RouterRegister 注册路由
func (c *Connection) RouterRegister(userID, deviceID string) {
	if c.localRouter != nil {
		c.localRouter.Register(userID, deviceID, c)
	}
	if c.distRouter != nil {
		c.distRouter.RegisterUser(c.ctx, userID, deviceID)
	}
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
func (c *Connection) SubmitUpstream(ctx context.Context, msg *types.Message) types.SubmitResult {
	if c.upstreamSender == nil {
		logger.Debug("upstream sender not set, skipping upstream message",
			zap.String("msg_id", msg.RequestID))
		return types.SubmitResult{Accepted: false, Reason: "not_initialized"}
	}

	// 从 Payload 提取 BizType
	var bizType gateway.BusinessType
	if bp, ok := msg.Payload.(*types.BusinessPayload); ok {
		bizType = bp.BizType.Proto()
	}

	job := types.UpstreamJob{
		Payload:  msg.Payload,
		ConnID:   c.connID,
		UserID:   c.userID,
		DeviceID: c.deviceID,
		BizType:  types.BusinessTypeFromProto(bizType),
	}
	return c.upstreamSender.Submit(ctx, job)
}

// GetConnID 获取连接ID
func (c *Connection) GetConnID() string {
	return c.connID
}

// GetUserInfo 获取用户信息
func (c *Connection) GetUserInfo() (string, string) {
	return c.userID, c.deviceID
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

// GetUpstreamSubmitter 获取上行提交器
func (c *Connection) GetUpstreamSubmitter() UpstreamSubmitter {
	return c
}

// UpstreamSubmitter 提交上行消息到 worker 池的接口
type UpstreamSubmitter interface {
	SubmitUpstream(ctx context.Context, msg *types.Message) types.SubmitResult
}
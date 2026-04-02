package sdk

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Client 客户端接口
type Client interface {
	// Connect 连接到服务器
	Connect() error

	// Auth 鉴权（同步）
	Auth(auth *AuthRequest) (*AuthResponse, error)

	// AuthAsync 鉴权（异步）
	AuthAsync(auth *AuthRequest) error

	// Subscribe 订阅主题（同步）
	Subscribe(topic string, qos uint32) error

	// SubscribeAsync 订阅主题（异步）
	SubscribeAsync(topic string, qos uint32) error

	// Unsubscribe 取消订阅（同步）
	Unsubscribe(topic string) error

	// UnsubscribeAsync 取消订阅（异步）
	UnsubscribeAsync(topic string) error

	// SendBusiness 发送业务消息（同步）
	SendBusiness(bizType BusinessType, msgID string, body []byte) error

	// SendBusinessAsync 发送业务消息（异步）
	SendBusinessAsync(bizType BusinessType, msgID string, body []byte)

	// Logout 登出
	Logout(reason uint32) error

	// Close 关闭客户端
	Close()

	// IsConnected 检查是否已连接
	IsConnected() bool

	// IsAuthenticated 检查是否已鉴权
	IsAuthenticated() bool

	// MessageChannel 返回消息通道
	MessageChannel() <-chan *Message

	// GetAuthResponse 获取鉴权响应
	GetAuthResponse() *AuthResponse
}

// BaseClient 客户端基类，包含公共逻辑
type BaseClient struct {
	Opts *ClientOptions

	state atomic.Int32 // ConnState

	authResp *AuthResponse
	authMu   sync.RWMutex

	sequenceID atomic.Uint64

	heartbeatMu      sync.Mutex
	heartbeatTicker  *time.Ticker
	heartbeatStop    chan struct{}

	msgCh chan *Message

	pendingResp map[string]chan *Message
	respMu      sync.Mutex

	closeMu sync.Mutex
	closed  bool

	ctx    context.Context
	cancel context.CancelFunc
}

// NewBaseClient 创建基础客户端
func NewBaseClient(opts ...Option) *BaseClient {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &BaseClient{
		Opts:          o,
		ctx:           ctx,
		cancel:        cancel,
		heartbeatStop: make(chan struct{}),
		msgCh:         make(chan *Message, 100),
		pendingResp:   make(map[string]chan *Message),
	}
}

// Ctx 获取上下文
func (c *BaseClient) Ctx() context.Context {
	return c.ctx
}

// Cancel 取消上下文
func (c *BaseClient) Cancel() {
	c.cancel()
}

// CloseMu 获取关闭锁
func (c *BaseClient) CloseMu() *sync.Mutex {
	return &c.closeMu
}

// IsClosed 检查是否已关闭
func (c *BaseClient) IsClosed() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closed
}

// SetClosed 设置关闭状态
func (c *BaseClient) SetClosed(closed bool) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	c.closed = closed
}

// State 获取连接状态
func (c *BaseClient) State() ConnState {
	return ConnState(c.state.Load())
}

// SetState 设置连接状态
func (c *BaseClient) SetState(state ConnState) {
	c.state.Store(int32(state))
}

// IsConnected 检查是否已连接
func (c *BaseClient) IsConnected() bool {
	s := c.state.Load()
	return int32(s) == int32(ConnStateConnected) || int32(s) == int32(ConnStateAuthenticated)
}

// IsAuthenticated 检查是否已鉴权
func (c *BaseClient) IsAuthenticated() bool {
	return c.state.Load() == int32(ConnStateAuthenticated)
}

// GetAuthResponse 获取鉴权响应
func (c *BaseClient) GetAuthResponse() *AuthResponse {
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	return c.authResp
}

// SetAuthResponse 设置鉴权响应
func (c *BaseClient) SetAuthResponse(resp *AuthResponse) {
	c.authMu.Lock()
	c.authResp = resp
	c.authMu.Unlock()
}

// MessageChannel 返回消息通道
func (c *BaseClient) MessageChannel() <-chan *Message {
	return c.msgCh
}

// MsgCh 返回消息通道（引用）
func (c *BaseClient) MsgCh() chan *Message {
	return c.msgCh
}

// NextSequenceID 获取下一个序列号
func (c *BaseClient) NextSequenceID() uint64 {
	return c.sequenceID.Add(1)
}

// WaitResponse 等待响应
func (c *BaseClient) WaitResponse(requestID string) chan *Message {
	ch := make(chan *Message, 1)
	c.respMu.Lock()
	c.pendingResp[requestID] = ch
	c.respMu.Unlock()
	return ch
}

// GetPendingResponse 获取等待的响应通道
func (c *BaseClient) GetPendingResponse(requestID string) chan *Message {
	c.respMu.Lock()
	defer c.respMu.Unlock()
	return c.pendingResp[requestID]
}

// RemovePendingResponse 移除等待的响应
func (c *BaseClient) RemovePendingResponse(requestID string) {
	c.respMu.Lock()
	delete(c.pendingResp, requestID)
	c.respMu.Unlock()
}

// NotifyPendingResponse 通知等待的响应
func (c *BaseClient) NotifyPendingResponse(requestID string, msg *Message) {
	if ch := c.GetPendingResponse(requestID); ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

// StartHeartbeat 启动心跳
func (c *BaseClient) StartHeartbeat(interval time.Duration) {
	c.heartbeatMu.Lock()
	defer c.heartbeatMu.Unlock()

	if c.heartbeatTicker != nil {
		return
	}

	c.heartbeatTicker = time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-c.heartbeatTicker.C:
				c.SendHeartbeat()
			case <-c.heartbeatStop:
				return
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

// StopHeartbeat 停止心跳
func (c *BaseClient) StopHeartbeat() {
	c.heartbeatMu.Lock()
	defer c.heartbeatMu.Unlock()

	if c.heartbeatTicker != nil {
		c.heartbeatTicker.Stop()
		c.heartbeatTicker = nil
	}
}

// SendHeartbeat 发送心跳（子类实现）
func (c *BaseClient) SendHeartbeat() {
	// 子类实现
}

// Close 关闭客户端
func (c *BaseClient) Close() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	if c.closed {
		return
	}
	c.closed = true

	c.cancel()
	c.StopHeartbeat()
	close(c.msgCh)
	close(c.heartbeatStop)
}

// GenRequestID 生成请求ID
func GenRequestID() string {
	return uuid.NewString()
}

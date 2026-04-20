package connection

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/consts"
	"github.com/vadam-zhan/long-gw/gateway/internal/logic/codec"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
)

// State encodes the connection lifecycle phase.
type State uint32

const (
	StateHandshaking State = iota // Waiting for AuthRequest
	StateActive                   // Authenticated, messages flowing
	StateClosing                  // Draining writeCh before shutdown
	StateClosed                   // Fully torn down
)

// Connection 连接实例，管理连接生命周期
// 负责：协议编解码、双goroutine模型、路由注册、上行提交
type Connection struct {
	// ---- Identity (immutable after activate)
	ConnID     string
	UserID     string
	AppID      string
	DeviceID   string
	DeviceType string
	BizCode    string

	// ---- Transport & codec
	tp      transport.Transport
	codec   codec.Codec           // encodes/decodes proto.Message <-> bytes 每连接独立 Negotiated(已协商) 实例
	writeCh chan *gateway.Message // cap=256, 背压边界

	// ---- State: Handshaking→Active→Closing→Closed
	state atomic.Uint32

	lastPingAt  atomic.Int64  // 心跳监控
	lastRecvSeq atomic.Uint64 // 断线重放起点

	nextSendSeq atomic.Uint64 // monotonically increasing server-side SeqID

	// 生命周期控制
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	doneCh chan struct{} // closed when WriteLoop exits

	ConnectedAt time.Time
}

// newConnection 创建连接实例
func newConnection(ctx context.Context, tp transport.Transport, codec codec.Codec) *Connection {
	ctx, cancel := context.WithCancel(ctx)
	c := &Connection{
		tp:      tp,
		codec:   codec,
		writeCh: make(chan *gateway.Message, consts.WriteChannelSize),
		ctx:     ctx,
		cancel:  cancel,
		doneCh:  make(chan struct{}),
	}
	c.state.Store(uint32(StateHandshaking))
	c.lastPingAt.Store(time.Now().UnixMilli())
	return c
}

// Submit enqueues a message for async delivery to the client.
// Returns false if the connection is not active or the writeCh is full.
// Callers must handle false (drop or store offline).
func (c *Connection) Submit(msg *gateway.Message) bool {
	if !c.IsActive() {
		return false
	}
	msg.SeqId = c.nextSendSeq.Add(1)
	select {
	case c.writeCh <- msg:
		return true
	default:
		return false // back-pressure: queue full
	}
}

func (c *Connection) State() State { return State(c.state.Load()) }

func (c *Connection) IsActive() bool { return c.State() == StateActive }

// Activate transitions the connection from Handshaking to Active.
// Called by AuthHandler after successful token validation.
func (c *Connection) Activate() {
	c.state.CompareAndSwap(uint32(StateHandshaking), uint32(StateActive))
}

func (c *Connection) GetConnID() string {
	return c.ConnID
}
func (c *Connection) GetUserID() string {
	return c.UserID
}
func (c *Connection) GetDeviceType() string {
	return c.DeviceType
}
func (c *Connection) RemoteAddr() string {
	return c.tp.RemoteAddr()
}

func (c *Connection) Close(kick *gateway.KickPayload) {

}

func (c *Connection) Run(
	onMessage func(conn *Connection, msg *gateway.Message),
	onClose func(conn *Connection),
) {
	defer func() {
		c.state.Store(uint32(StateClosed))
		c.tp.Close()
		close(c.doneCh)
		if onClose != nil {
			onClose(c)
		}
	}()

	writeLoopDone := make(chan struct{})
	go func() {
		defer close(writeLoopDone)
		c.writeLoop()
	}()

	c.readLoop(onMessage)

	<-writeLoopDone
}

// ReadLoop 读取并处理消息
func (c *Connection) readLoop(onMessage func(*Connection, *gateway.Message)) {
	for {
		select {
		case <-c.ctx.Done():
			slog.Debug("readLoop exit",
				"remote", c.tp.RemoteAddr())
			return

		default:
			// 设置读取超时
			if err := c.tp.SetReadDeadline(time.Now().Add(consts.HeartbeatTimeout)); err != nil {
				slog.Error("set read deadline failed",
					"error", err,
					"remote", c.tp.RemoteAddr())
				return
			}

			// 读取原始数据
			raw, err := c.tp.Read(c.ctx)
			if err != nil {
				slog.Error("read message failed",
					"error", err,
					"remote", c.tp.RemoteAddr())
				return
			}

			// Touch heartbeat timestamp on any inbound frame (Ping or data).
			c.lastPingAt.Store(time.Now().UnixMilli())

			msg, err := c.codec.Decode(raw)
			if err != nil {
				slog.Error("connection: decode error", "connID", c.ConnID, "error", err, "remote", c.tp.RemoteAddr())
				continue // skip malformed frames, do not close connection
			}

			if msg.SeqId > 0 {
				c.lastRecvSeq.Store(msg.SeqId)
			}
			if isExpired(msg) {
				slog.Error("connection: drop expired", "mid", msg.MsgId, "conn", c.ConnID)
				continue
			}

			onMessage(c, msg)
		}
	}
}

// isExpired reports whether a message has passed its absolute expiry time.
func isExpired(msg *gateway.Message) bool {
	return msg.ExpireAt > 0 && time.Now().UnixMilli() > msg.ExpireAt
}

// WriteLoop 处理下行消息写入
func (c *Connection) writeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			slog.Debug("writeLoop exit",
				"remote", c.tp.RemoteAddr())
			for {
				select {
				case msg := <-c.writeCh:
					c.encodeAndWrite(msg)
				default:
					return
				}
			}

		case msg, ok := <-c.writeCh:
			if !ok {
				return
			}

			if err := c.encodeAndWrite(msg); err != nil {
				slog.Warn("connection: write error",
					"connID", c.ConnID,
					"error", err,
					"remote", c.tp.RemoteAddr())
				return
			}
		}
	}
}

func (c *Connection) Send(msg *gateway.Message) error {
	if !c.IsActive() {
		return fmt.Errorf("connection: not active")
	}
	select {
	case c.writeCh <- msg:
		return nil
	default:
		return fmt.Errorf("connection: write buffer full")
	}
}

func (c *Connection) encodeAndWrite(msg *gateway.Message) error {
	data, err := c.codec.Encode(msg)
	if err != nil {
		return fmt.Errorf("connection: encode err: %w", err)
	}
	return c.tp.Write(c.ctx, data)
}

// ─────────────────────────────────────────────────────────────────────────────
// HeartbeatWatchdog — runs as a separate goroutine per connection
// ─────────────────────────────────────────────────────────────────────────────

// HeartbeatWatchdog closes the connection if no inbound frame arrives within timeout.
// Start this goroutine after the connection enters Active state.
func (c *Connection) HeartbeatWatchdog(timeout time.Duration) {
	halfTimeout := timeout / 2
	ticker := time.NewTicker(halfTimeout)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			since := time.Since(time.UnixMilli(c.lastPingAt.Load()))
			if since > timeout {
				slog.Info("connection: heartbeat timeout",
					"conn", c.ConnID,
					"uid", c.UserID,
					"since", since,
				)
				c.Close(&gateway.KickPayload{
					Code:   4001,
					Reason: "heartbeat timeout",
				})
				return
			}
		}
	}
}

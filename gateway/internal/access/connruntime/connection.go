package connruntime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	gatewayv1 "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/access/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/consts"
	"github.com/vadam-zhan/long-gw/gateway/internal/protocol/codec"
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
	codec   codec.Codec             // encodes/decodes proto.Message <-> bytes 每连接独立 Negotiated(已协商) 实例
	writeCh chan *gatewayv1.Message // cap=256, 背压边界

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
		writeCh: make(chan *gatewayv1.Message, consts.WriteChannelSize),
		ctx:     ctx,
		cancel:  cancel,
		doneCh:  make(chan struct{}),
	}
	c.state.Store(uint32(StateHandshaking))
	c.lastPingAt.Store(time.Now().UnixMilli())
	return c
}

func (c *Connection) Run(
	onMessage func(conn *Connection, msg *gatewayv1.Message),
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

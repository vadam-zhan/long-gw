package connruntime

import (
	"fmt"
	"log/slog"
	"time"

	gatewayv1 "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
)

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

// Submit enqueues a message for async delivery to the client.
// Returns false if the connection is not active or the writeCh is full.
// Callers must handle false (drop or store offline).
func (c *Connection) Submit(msg *gatewayv1.Message) bool {
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

func (c *Connection) Send(msg *gatewayv1.Message) error {
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

func (c *Connection) encodeAndWrite(msg *gatewayv1.Message) error {
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
				c.Close(&gatewayv1.KickRequest{
					Code:   4001,
					Reason: "heartbeat timeout",
				})
				return
			}
		}
	}
}

package connruntime

import (
	"log/slog"
	"time"

	gatewayv1 "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/consts"
)

// ReadLoop 读取并处理消息
func (c *Connection) readLoop(onMessage func(*Connection, *gatewayv1.Message)) {
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
func isExpired(msg *gatewayv1.Message) bool {
	return msg.ExpireAt > 0 && time.Now().UnixMilli() > msg.ExpireAt
}

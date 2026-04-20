package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
)

// WsHandler WebSocket 处理
type WsHandler struct {
	upgrader websocket.Upgrader
}

// NewWsHandler 创建 WsHandler
func NewWsHandler() *WsHandler {
	return &WsHandler{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// UpgradeHandler 处理 WebSocket 升级
func (h *WsHandler) UpgradeHandler(c *gin.Context) *websocket.Conn {
	rawConn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "error", err)
		return nil
	}
	return rawConn
}

// CreateTransport 创建 Transport
func (h *WsHandler) CreateTransport(rawConn *websocket.Conn) transport.Transport {
	return transport.NewWSTransport(rawConn)
}

package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// WSTransport WebSocket transport implementation
type WSTransport struct {
	conn *websocket.Conn
}

// NewWSTransport creates a new WebSocket transport
func NewWSTransport(conn *websocket.Conn) *WSTransport {
	return &WSTransport{conn: conn}
}

func (w *WSTransport) Read(ctx context.Context) ([]byte, error) {
	msgType, data, err := w.conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read websocket failed: %w", err)
	}
	if msgType != websocket.BinaryMessage {
		return nil, errors.New("only binary message supported")
	}
	if len(data) > MaxFrame {
		return nil, fmt.Errorf("protobuf body too large: %d", len(data))
	}
	return data, nil
}

func (w *WSTransport) Write(ctx context.Context, data []byte) error {
	return w.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (w *WSTransport) Close() {
	w.conn.Close()
}

func (w *WSTransport) RemoteAddr() string {
	return w.conn.RemoteAddr().String()
}

func (c *WSTransport) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

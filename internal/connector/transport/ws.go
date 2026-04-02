package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	gateway "github.com/vadam-zhan/long-gw/api/proto/v1"
	"google.golang.org/protobuf/proto"
)

// WSTransport WebSocket transport implementation
type WSTransport struct {
	conn *websocket.Conn
}

func NewWSTrasnport(conn *websocket.Conn) *WSTransport {
	return &WSTransport{conn: conn}
}

func (c *WSTransport) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (w *WSTransport) Read(ctx context.Context) (*gateway.ClientSignal, error) {
	// Read message
	msgType, data, err := w.conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read websocket failed: %w", err)
	}
	if msgType != websocket.BinaryMessage {
		return nil, errors.New("only binary message supported")
	}
	if len(data) > MaxBodyLen {
		return nil, fmt.Errorf("protobuf body too large: %d", len(data))
	}
	clientSignal := clientSignalPool.Get().(*gateway.ClientSignal)
	clientSignal.Reset()
	if err := proto.Unmarshal(data, clientSignal); err != nil {
		clientSignalPool.Put(clientSignal)
		return nil, fmt.Errorf("unmarshal protobuf failed: %w", err)
	}
	return clientSignal, nil
}

func (w *WSTransport) Write(ctx context.Context, data *gateway.ClientSignal) error {
	protobufBody, err := marshalOpts.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal protobuf failed: %w", err)
	}

	return w.conn.WriteMessage(websocket.BinaryMessage, protobufBody)
}

func (w *WSTransport) Close() error {
	return w.conn.Close()
}

func (w *WSTransport) RemoteAddr() string {
	return w.conn.RemoteAddr().String()
}

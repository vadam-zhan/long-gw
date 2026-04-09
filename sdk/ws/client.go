// Package ws 实现了基于WebSocket协议的长连接网关SDK客户端
package ws

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/sdk"
	"google.golang.org/protobuf/proto"

	"github.com/gorilla/websocket"
)

var marshalOpts = proto.MarshalOptions{
	UseCachedSize: true,
}

// Transport WebSocket传输层
type Transport struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// NewTransport 创建WebSocket传输层
func NewTransport(conn *websocket.Conn) *Transport {
	return &Transport{conn: conn}
}

// SetReadDeadline 设置读取超时
func (t *Transport) SetReadDeadline(deadline time.Time) error {
	return t.conn.SetReadDeadline(deadline)
}

// SetWriteDeadline 设置写入超时
func (t *Transport) SetWriteDeadline(deadline time.Time) error {
	return t.conn.SetWriteDeadline(deadline)
}

// Read 读取消息
func (t *Transport) Read(ctx context.Context) (*gateway.ClientSignal, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		_, reader, err := t.conn.NextReader()
		if err != nil {
			return nil, fmt.Errorf("get next reader failed: %w", err)
		}

		// 读取全部数据
		data := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				data = append(data, buf[:n]...)
			}
			if err != nil {
				break
			}
		}

		if len(data) == 0 {
			continue
		}

		// 解析protobuf
		clientSignal := &gateway.ClientSignal{}
		if err := proto.Unmarshal(data, clientSignal); err != nil {
			return nil, fmt.Errorf("unmarshal protobuf failed: %w", err)
		}

		return clientSignal, nil
	}
}

// Write 写入消息
func (t *Transport) Write(ctx context.Context, data *gateway.ClientSignal) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 序列化protobuf
	protobufBody, err := marshalOpts.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal protobuf failed: %w", err)
	}

	t.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := t.conn.WriteMessage(websocket.BinaryMessage, protobufBody); err != nil {
		return fmt.Errorf("write websocket message failed: %w", err)
	}

	return nil
}

// Close 关闭连接
func (t *Transport) Close() error {
	return t.conn.Close()
}

// RemoteAddr 获取远端地址
func (t *Transport) RemoteAddr() string {
	return t.conn.RemoteAddr().String()
}

// Client WebSocket客户端实现
type Client struct {
	*sdk.BaseClient
	transport *Transport
	conn      *websocket.Conn
}

// NewClient 创建WebSocket客户端
func NewClient(opts ...sdk.Option) *Client {
	return &Client{
		BaseClient: sdk.NewBaseClient(opts...),
	}
}

// getWsURL 将 HTTP 地址转换为 WS 地址
func getWsURL(addr string) string {
	if addr == "" {
		return ""
	}

	// 检查是否已经是 ws:// 或 wss://
	if len(addr) > 2 {
		if addr[:2] == "ws" {
			return addr
		}
	}

	// 替换 http:// 为 ws://
	u, err := url.Parse(addr)
	if err != nil {
		return "ws://" + addr
	}

	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}

	if u.Port() == "" {
		if u.Scheme == "ws" {
			u.Host = u.Host + ":80"
		} else {
			u.Host = u.Host + ":443"
		}
	}

	return u.String()
}

// Connect 连接到服务器
func (c *Client) Connect() error {
	if c.IsClosed() {
		return fmt.Errorf("client already closed")
	}

	// 连接
	c.SetState(sdk.ConnStateConnecting)

	wsURL := getWsURL(c.Opts.Addr + "/v1/ws/connect")

	var header http.Header

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		c.SetState(sdk.ConnStateDisconnected)
		if c.Opts.ConnectHandler != nil {
			c.Opts.ConnectHandler(sdk.ConnStateDisconnected, err)
		}
		if resp != nil {
			resp.Body.Close()
		}
		return fmt.Errorf("dial websocket failed: %w", err)
	}

	c.conn = conn
	c.transport = NewTransport(conn)
	c.SetState(sdk.ConnStateConnected)
	if c.Opts.ConnectHandler != nil {
		c.Opts.ConnectHandler(sdk.ConnStateConnected, nil)
	}

	// 启动读取goroutine
	go c.readLoop()

	return nil
}

// readLoop 读取循环
func (c *Client) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("readLoop panic: %v\n", r)
		}
		c.Close()
	}()

	for {
		select {
		case <-c.Ctx().Done():
			return
		default:
			c.transport.SetReadDeadline(time.Now().Add(c.Opts.ReadTimeout))
			signal, err := c.transport.Read(c.Ctx())
			if err != nil {
				if c.Ctx().Err() != nil {
					return
				}
				c.SetState(sdk.ConnStateDisconnected)
				if c.Opts.ConnectHandler != nil {
					c.Opts.ConnectHandler(sdk.ConnStateDisconnected, err)
				}
				return
			}
			c.handleSignal(signal)
		}
	}
}

// handleSignal 处理信号
func (c *Client) handleSignal(signal *gateway.ClientSignal) {
	msg := sdk.DecodeClientSignal(signal)

	switch signal.SignalType {
	case gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE:
		resp := sdk.DecodeAuthResponse(signal)
		c.SetAuthResponse(resp)

		if resp != nil && resp.Result {
			c.SetState(sdk.ConnStateAuthenticated)
			c.StartHeartbeat(resp.HeartbeatInterval, c)
			if c.Opts.ConnectHandler != nil {
				c.Opts.ConnectHandler(sdk.ConnStateAuthenticated, nil)
			}
		} else {
			c.SetState(sdk.ConnStateAuthenticationFailed)
			if c.Opts.ConnectHandler != nil {
				var errMsg string
				if resp != nil {
					errMsg = resp.Msg
				}
				c.Opts.ConnectHandler(sdk.ConnStateAuthenticationFailed, fmt.Errorf("%s", errMsg))
			}
			c.Close()
		}
		c.NotifyPendingResponse(msg.RequestID, msg)

	case gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PONG:
		// 心跳响应，忽略

	case gateway.SignalType_SIGNAL_TYPE_BUSINESS_DOWN:
		bizMsg := sdk.DecodeBusinessDown(signal)
		if bizMsg != nil {
			select {
			case c.MsgCh() <- bizMsg:
			default:
			}
			if c.Opts.MessageHandler != nil {
				c.Opts.MessageHandler(bizMsg)
			}
		}

	default:
		c.NotifyPendingResponse(msg.RequestID, msg)
	}
}

// SendHeartbeat 发送心跳
func (c *Client) SendHeartbeat() {
	if c.transport == nil {
		return
	}
	requestID := sdk.GenRequestID()
	signal := sdk.EncodeHeartbeatPing(requestID, sdk.CurrentTimestamp())

	ctx, cancel := context.WithTimeout(c.Ctx(), c.Opts.WriteTimeout)
	defer cancel()

	if err := c.transport.Write(ctx, signal); err != nil {
		c.SetState(sdk.ConnStateDisconnected)
		if c.Opts.ConnectHandler != nil {
			c.Opts.ConnectHandler(sdk.ConnStateDisconnected, err)
		}
	}
}

// Auth 鉴权（同步）
func (c *Client) Auth(auth *sdk.AuthRequest) (*sdk.AuthResponse, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected")
	}

	requestID := sdk.GenRequestID()
	signal := sdk.EncodeAuthRequest(requestID, auth)

	respCh := c.WaitResponse(requestID)
	defer c.RemovePendingResponse(requestID)

	ctx, cancel := context.WithTimeout(c.Ctx(), c.Opts.AuthTimeout)
	defer cancel()

	if err := c.transport.Write(ctx, signal); err != nil {
		return nil, fmt.Errorf("send auth request failed: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("auth timeout")
	case resp := <-respCh:
		if resp.Type != sdk.SignalTypeAuthResponse {
			return nil, fmt.Errorf("unexpected response type: %v", resp.Type)
		}
		return c.GetAuthResponse(), nil
	}
}

// AuthAsync 鉴权（异步）
func (c *Client) AuthAsync(auth *sdk.AuthRequest) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected")
	}

	requestID := sdk.GenRequestID()
	signal := sdk.EncodeAuthRequest(requestID, auth)

	ctx, cancel := context.WithTimeout(c.Ctx(), c.Opts.AuthTimeout)
	defer cancel()

	return c.transport.Write(ctx, signal)
}

// Subscribe 订阅主题（同步）
func (c *Client) Subscribe(topic string, qos uint32) error {
	if !c.IsAuthenticated() {
		return fmt.Errorf("not authenticated")
	}

	requestID := sdk.GenRequestID()
	signal := sdk.EncodeSubscribeRequest(requestID, &sdk.SubscribeRequest{
		Topic: topic,
		Qos:   qos,
	})

	respCh := c.WaitResponse(requestID)
	defer c.RemovePendingResponse(requestID)

	ctx, cancel := context.WithTimeout(c.Ctx(), 5*time.Second)
	defer cancel()

	if err := c.transport.Write(ctx, signal); err != nil {
		return fmt.Errorf("send subscribe request failed: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("subscribe timeout")
	case resp := <-respCh:
		if resp.Type != sdk.SignalTypeSubscribeResponse {
			return fmt.Errorf("unexpected response type: %v", resp.Type)
		}
		return nil
	}
}

// SubscribeAsync 订阅主题（异步）
func (c *Client) SubscribeAsync(topic string, qos uint32) error {
	if !c.IsAuthenticated() {
		return fmt.Errorf("not authenticated")
	}

	requestID := sdk.GenRequestID()
	signal := sdk.EncodeSubscribeRequest(requestID, &sdk.SubscribeRequest{
		Topic: topic,
		Qos:   qos,
	})

	go func() {
		ctx, cancel := context.WithTimeout(c.Ctx(), 5*time.Second)
		defer cancel()
		c.transport.Write(ctx, signal)
	}()

	return nil
}

// Unsubscribe 取消订阅（同步）
func (c *Client) Unsubscribe(topic string) error {
	if !c.IsAuthenticated() {
		return fmt.Errorf("not authenticated")
	}

	requestID := sdk.GenRequestID()
	signal := sdk.EncodeUnsubscribeRequest(requestID, topic)

	respCh := c.WaitResponse(requestID)
	defer c.RemovePendingResponse(requestID)

	ctx, cancel := context.WithTimeout(c.Ctx(), 5*time.Second)
	defer cancel()

	if err := c.transport.Write(ctx, signal); err != nil {
		return fmt.Errorf("send unsubscribe request failed: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("unsubscribe timeout")
	case resp := <-respCh:
		if resp.Type != sdk.SignalTypeUnsubscribeResponse {
			return fmt.Errorf("unexpected response type: %v", resp.Type)
		}
		return nil
	}
}

// UnsubscribeAsync 取消订阅（异步）
func (c *Client) UnsubscribeAsync(topic string) error {
	if !c.IsAuthenticated() {
		return fmt.Errorf("not authenticated")
	}

	requestID := sdk.GenRequestID()
	signal := sdk.EncodeUnsubscribeRequest(requestID, topic)

	go func() {
		ctx, cancel := context.WithTimeout(c.Ctx(), 5*time.Second)
		defer cancel()
		c.transport.Write(ctx, signal)
	}()

	return nil
}

// SendBusiness 发送业务消息（同步）
func (c *Client) SendBusiness(bizType sdk.BusinessType, msgID string, body []byte) error {
	if !c.IsAuthenticated() {
		return fmt.Errorf("not authenticated")
	}

	requestID := sdk.GenRequestID()
	sequenceID := c.NextSequenceID()

	signal := sdk.EncodeBusinessUp(requestID, sequenceID, &sdk.BusinessMessage{
		MsgID:   msgID,
		BizType: bizType,
		Body:    body,
	})

	ctx, cancel := context.WithTimeout(c.Ctx(), c.Opts.WriteTimeout)
	defer cancel()

	return c.transport.Write(ctx, signal)
}

// SendBusinessAsync 发送业务消息（异步）
func (c *Client) SendBusinessAsync(bizType sdk.BusinessType, msgID string, body []byte) {
	if !c.IsAuthenticated() {
		return
	}

	go func() {
		requestID := sdk.GenRequestID()
		sequenceID := c.NextSequenceID()

		signal := sdk.EncodeBusinessUp(requestID, sequenceID, &sdk.BusinessMessage{
			MsgID:   msgID,
			BizType: bizType,
			Body:    body,
		})

		ctx, cancel := context.WithTimeout(c.Ctx(), c.Opts.WriteTimeout)
		defer cancel()
		c.transport.Write(ctx, signal)
	}()
}

// Logout 登出
func (c *Client) Logout(reason uint32) error {
	if !c.IsAuthenticated() {
		return fmt.Errorf("not authenticated")
	}

	c.StopHeartbeat()

	requestID := sdk.GenRequestID()
	signal := sdk.EncodeLogoutRequest(requestID, reason)

	ctx, cancel := context.WithTimeout(c.Ctx(), 5*time.Second)
	defer cancel()

	return c.transport.Write(ctx, signal)
}

// Close 关闭客户端
func (c *Client) Close() {
	c.BaseClient.Close()
	if c.transport != nil {
		c.transport.Close()
	}
}

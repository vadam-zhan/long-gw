// Package tcp 实现了基于TCP协议的长连接网关SDK客户端
package tcp

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/sdk"
	"google.golang.org/protobuf/proto"
)

const (
	// TLV header format
	// +--------+--------+--------+--------+
	// |  magic |  ver   |  type  | length |
	// +--------+--------+--------+--------+
	//   2 byte   1 byte   1 byte   4 bytes
	Magic      = 0x1234
	HeaderLen  = 8
	MaxBodyLen = 1024 * 1024 // 1MB
	Version    = 0x01
)

var marshalOpts = proto.MarshalOptions{
	UseCachedSize: true,
}

// Transport TCP传输层
type Transport struct {
	conn      net.Conn
	mu        sync.Mutex
	readMu    sync.Mutex
	packetBuf []byte
}

// NewTransport 创建TCP传输层
func NewTransport(conn net.Conn) *Transport {
	return &Transport{
		conn:      conn,
		packetBuf: make([]byte, 0, 4096),
	}
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
	t.readMu.Lock()
	defer t.readMu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// 检查半包缓存是否有完整包头
		if len(t.packetBuf) < HeaderLen {
			readBuf := make([]byte, 4096)
			t.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			n, err := t.conn.Read(readBuf)
			if err != nil {
				if err == io.EOF {
					return nil, fmt.Errorf("connection closed")
				}
				return nil, fmt.Errorf("read tcp failed: %w", err)
			}
			t.packetBuf = append(t.packetBuf, readBuf[:n]...)
			continue
		}

		// 解析TLV包头
		magic := binary.BigEndian.Uint16(t.packetBuf[:2])
		if magic != Magic {
			return nil, fmt.Errorf("invalid magic: 0x%x", magic)
		}

		// 解析包体长度
		protobufLen := binary.BigEndian.Uint32(t.packetBuf[4:8])
		if protobufLen > MaxBodyLen {
			return nil, fmt.Errorf("protobuf body too large: %d", protobufLen)
		}

		totalPacketLen := HeaderLen + int(protobufLen)
		if len(t.packetBuf) < totalPacketLen {
			// 包体不完整，继续读取
			readBuf := make([]byte, 4096)
			t.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			n, err := t.conn.Read(readBuf)
			if err != nil {
				if err == io.EOF {
					return nil, fmt.Errorf("connection closed")
				}
				return nil, fmt.Errorf("read tcp failed: %w", err)
			}
			t.packetBuf = append(t.packetBuf, readBuf[:n]...)
			continue
		}

		// 提取完整包
		protobufBody := t.packetBuf[HeaderLen:totalPacketLen]
		t.packetBuf = t.packetBuf[totalPacketLen:]

		// 解析protobuf
		clientSignal := &gateway.ClientSignal{}
		if err := proto.Unmarshal(protobufBody, clientSignal); err != nil {
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

	// 构造TLV包
	packet := make([]byte, HeaderLen+len(protobufBody))
	binary.BigEndian.PutUint16(packet[:2], Magic)
	packet[2] = Version
	binary.BigEndian.PutUint32(packet[4:8], uint32(len(protobufBody)))
	copy(packet[HeaderLen:], protobufBody)

	t.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = t.conn.Write(packet)
	if err != nil {
		return fmt.Errorf("write tcp failed: %w", err)
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

// Client TCP客户端实现
type Client struct {
	*sdk.BaseClient
	transport *Transport
	conn      net.Conn
}

// NewClient 创建TCP客户端
func NewClient(opts ...sdk.Option) *Client {
	return &Client{
		BaseClient: sdk.NewBaseClient(opts...),
	}
}

// Connect 连接到服务器
func (c *Client) Connect() error {
	c.CloseMu().Lock()
	if c.IsClosed() {
		c.CloseMu().Unlock()
		return fmt.Errorf("client already closed")
	}
	c.CloseMu().Unlock()

	// 连接
	c.SetState(sdk.ConnStateConnecting)
	conn, err := net.DialTimeout("tcp", c.Opts.Addr, c.Opts.ConnectTimeout)
	if err != nil {
		c.SetState(sdk.ConnStateDisconnected)
		if c.Opts.ConnectHandler != nil {
			c.Opts.ConnectHandler(sdk.ConnStateDisconnected, err)
		}
		return fmt.Errorf("dial tcp failed: %w", err)
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
			c.StartHeartbeat(time.Duration(resp.HeartbeatInterval) * time.Second)
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

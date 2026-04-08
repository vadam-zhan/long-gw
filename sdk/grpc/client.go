// Package grpc 实现了后端服务通过gRPC向网关推送消息的客户端
package grpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/sdk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// PushType 推送类型
type PushType = gateway.DownStreamPushType

const (
	PushTypeUnspecified PushType = gateway.DownStreamPushType_DownStreamPushType_UNSPECIFIED
	PushTypeSingle      PushType = gateway.DownStreamPushType_DownStreamPushType_SINGLE
	PushTypeGroup       PushType = gateway.DownStreamPushType_DownStreamPushType_GROUP
)

// PushRequest 推送请求
type PushRequest struct {
	Receiver     string           // 接收者 ID (userID)
	Payload      []byte           // 业务载荷
	PushType     PushType         // 推送类型 (单播/组播)
	BusinessType sdk.BusinessType // 业务类型
}

// PushResponse 推送响应
type PushResponse struct {
	Success bool
	Message string
}

// ClientOptions gRPC客户端配置
type ClientOptions struct {
	Addr        string        // gRPC服务器地址 (host:port)
	Timeout     time.Duration // 调用超时 (默认 5s)
	DialOptions []grpc.DialOption
}

// Option 配置选项函数
type Option func(*ClientOptions)

var defaultDialOptions = []grpc.DialOption{
	grpc.WithTransportCredentials(insecure.NewCredentials()),
	grpc.WithBlock(),
}

// WithAddr 设置服务器地址
func WithAddr(addr string) Option {
	return func(o *ClientOptions) {
		o.Addr = addr
	}
}

// WithTimeout 设置调用超时
func WithTimeout(timeout time.Duration) Option {
	return func(o *ClientOptions) {
		o.Timeout = timeout
	}
}

// WithDialOption 设置gRPC拨号选项
func WithDialOption(opt grpc.DialOption) Option {
	return func(o *ClientOptions) {
		o.DialOptions = append(o.DialOptions, opt)
	}
}

func defaultOptions() *ClientOptions {
	return &ClientOptions{
		Timeout:     5 * time.Second,
		DialOptions: defaultDialOptions,
	}
}

// Client gRPC客户端
type Client struct {
	opts   *ClientOptions
	conn   *grpc.ClientConn
	client gateway.GatewayClient

	closeMu sync.Mutex
	closed  bool
}

// NewClient 创建gRPC客户端
func NewClient(opts ...Option) (*Client, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	if o.Addr == "" {
		return nil, fmt.Errorf("addr is required")
	}

	// 合并拨号选项
	dialOpts := o.DialOptions
	if dialOpts == nil {
		dialOpts = defaultDialOptions
	}

	conn, err := grpc.Dial(o.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial grpc failed: %w", err)
	}

	return &Client{
		opts:   o,
		conn:   conn,
		client: gateway.NewGatewayClient(conn),
	}, nil
}

// Push 推送消息（同步）
func (c *Client) Push(ctx context.Context, req *PushRequest) (*PushResponse, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("client not connected")
	}

	protoReq := &gateway.PushMessageReq{
		Receiver:     req.Receiver,
		Payload:      req.Payload,
		PushType:     toProtoPushType(req.PushType),
		BusinessType: req.BusinessType.ToProto(),
	}

	timeout := c.opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := c.client.PushMessage(ctx, protoReq)
	if err != nil {
		return nil, fmt.Errorf("push message failed: %w", err)
	}

	return &PushResponse{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

// PushAsync 推送消息（异步）
func (c *Client) PushAsync(req *PushRequest) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.opts.Timeout)
		defer cancel()
		c.Push(ctx, req)
	}()
}

// Close 关闭客户端
func (c *Client) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// toProtoPushType 转换推送类型
func toProtoPushType(t PushType) gateway.DownStreamPushType {
	switch t {
	case PushTypeSingle:
		return gateway.DownStreamPushType_DownStreamPushType_SINGLE
	case PushTypeGroup:
		return gateway.DownStreamPushType_DownStreamPushType_GROUP
	default:
		return gateway.DownStreamPushType_DownStreamPushType_UNSPECIFIED
	}
}

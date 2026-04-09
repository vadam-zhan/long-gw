package sdk

import (
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// ===============================
// 业务类型定义
// ===============================

// BusinessType 业务类型
type BusinessType int32

const (
	BusinessTypeUnknown BusinessType = 0
	BusinessTypeIM      BusinessType = 1
	BusinessTypeLIVE    BusinessType = 2
	BusinessTypeMESSAGE BusinessType = 3
)

func (t BusinessType) String() string {
	switch t {
	case BusinessTypeIM:
		return "im"
	case BusinessTypeLIVE:
		return "live"
	case BusinessTypeMESSAGE:
		return "message"
	default:
		return "unknown"
	}
}

func (t BusinessType) ToProto() gateway.BusinessType {
	switch t {
	case BusinessTypeIM:
		return gateway.BusinessType_BusinessType_IM
	case BusinessTypeLIVE:
		return gateway.BusinessType_BusinessType_LIVE
	case BusinessTypeMESSAGE:
		return gateway.BusinessType_BusinessType_MESSAGE
	default:
		return gateway.BusinessType_BusinessType_UNSPECIFIED
	}
}

func BusinessTypeFromProto(p gateway.BusinessType) BusinessType {
	switch p {
	case gateway.BusinessType_BusinessType_IM:
		return BusinessTypeIM
	case gateway.BusinessType_BusinessType_LIVE:
		return BusinessTypeLIVE
	case gateway.BusinessType_BusinessType_MESSAGE:
		return BusinessTypeMESSAGE
	default:
		return BusinessTypeUnknown
	}
}

// ===============================
// 信令类型定义
// ===============================

// SignalType 信令类型
type SignalType int32

const (
	SignalTypeUnknown             SignalType = 0
	SignalTypeHeartbeatPing       SignalType = 1
	SignalTypeHeartbeatPong       SignalType = 2
	SignalTypeAuthRequest         SignalType = 3
	SignalTypeAuthResponse        SignalType = 4
	SignalTypeMessageAck          SignalType = 5
	SignalTypeLogoutRequest       SignalType = 7
	SignalTypeLogoutResponse      SignalType = 8
	SignalTypeSubscribeRequest    SignalType = 9
	SignalTypeSubscribeResponse   SignalType = 10
	SignalTypeUnsubscribeRequest  SignalType = 11
	SignalTypeUnsubscribeResponse SignalType = 12
	SignalTypeBusinessUp          SignalType = 100
	SignalTypeBusinessDown        SignalType = 101
)

func (t SignalType) String() string {
	switch t {
	case SignalTypeHeartbeatPing:
		return "HeartbeatPing"
	case SignalTypeHeartbeatPong:
		return "HeartbeatPong"
	case SignalTypeAuthRequest:
		return "AuthRequest"
	case SignalTypeAuthResponse:
		return "AuthResponse"
	case SignalTypeMessageAck:
		return "MessageAck"
	case SignalTypeLogoutRequest:
		return "LogoutRequest"
	case SignalTypeLogoutResponse:
		return "LogoutResponse"
	case SignalTypeSubscribeRequest:
		return "SubscribeRequest"
	case SignalTypeSubscribeResponse:
		return "SubscribeResponse"
	case SignalTypeUnsubscribeRequest:
		return "UnsubscribeRequest"
	case SignalTypeUnsubscribeResponse:
		return "UnsubscribeResponse"
	case SignalTypeBusinessUp:
		return "BusinessUp"
	case SignalTypeBusinessDown:
		return "BusinessDown"
	default:
		return "Unknown"
	}
}

func (t SignalType) ToProto() gateway.SignalType {
	switch t {
	case SignalTypeHeartbeatPing:
		return gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PING
	case SignalTypeHeartbeatPong:
		return gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PONG
	case SignalTypeAuthRequest:
		return gateway.SignalType_SIGNAL_TYPE_AUTH_REQUEST
	case SignalTypeAuthResponse:
		return gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE
	case SignalTypeMessageAck:
		return gateway.SignalType_SIGNAL_TYPE_MESSAGE_ACK
	case SignalTypeLogoutRequest:
		return gateway.SignalType_SIGNAL_TYPE_LOGOUT_REQUEST
	case SignalTypeLogoutResponse:
		return gateway.SignalType_SIGNAL_TYPE_LOGOUT_RESPONSE
	case SignalTypeSubscribeRequest:
		return gateway.SignalType_SIGNAL_TYPE_SUBSCRIBE_REQUEST
	case SignalTypeSubscribeResponse:
		return gateway.SignalType_SIGNAL_TYPE_SUBSCRIBE_RESPONSE
	case SignalTypeUnsubscribeRequest:
		return gateway.SignalType_SIGNAL_TYPE_UNSUBSCRIBE_REQUEST
	case SignalTypeUnsubscribeResponse:
		return gateway.SignalType_SIGNAL_TYPE_UNSUBSCRIBE_RESPONSE
	case SignalTypeBusinessUp:
		return gateway.SignalType_SIGNAL_TYPE_BUSINESS_UP
	case SignalTypeBusinessDown:
		return gateway.SignalType_SIGNAL_TYPE_BUSINESS_DOWN
	default:
		return gateway.SignalType_SIGNAL_TYPE_UNSPECIFIED
	}
}

func SignalTypeFromProto(p gateway.SignalType) SignalType {
	switch p {
	case gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PING:
		return SignalTypeHeartbeatPing
	case gateway.SignalType_SIGNAL_TYPE_HEARTBEAT_PONG:
		return SignalTypeHeartbeatPong
	case gateway.SignalType_SIGNAL_TYPE_AUTH_REQUEST:
		return SignalTypeAuthRequest
	case gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE:
		return SignalTypeAuthResponse
	case gateway.SignalType_SIGNAL_TYPE_MESSAGE_ACK:
		return SignalTypeMessageAck
	case gateway.SignalType_SIGNAL_TYPE_LOGOUT_REQUEST:
		return SignalTypeLogoutRequest
	case gateway.SignalType_SIGNAL_TYPE_LOGOUT_RESPONSE:
		return SignalTypeLogoutResponse
	case gateway.SignalType_SIGNAL_TYPE_SUBSCRIBE_REQUEST:
		return SignalTypeSubscribeRequest
	case gateway.SignalType_SIGNAL_TYPE_SUBSCRIBE_RESPONSE:
		return SignalTypeSubscribeResponse
	case gateway.SignalType_SIGNAL_TYPE_UNSUBSCRIBE_REQUEST:
		return SignalTypeUnsubscribeRequest
	case gateway.SignalType_SIGNAL_TYPE_UNSUBSCRIBE_RESPONSE:
		return SignalTypeUnsubscribeResponse
	case gateway.SignalType_SIGNAL_TYPE_BUSINESS_UP:
		return SignalTypeBusinessUp
	case gateway.SignalType_SIGNAL_TYPE_BUSINESS_DOWN:
		return SignalTypeBusinessDown
	default:
		return SignalTypeUnknown
	}
}

// ===============================
// 连接状态定义
// ===============================

// ConnState 连接状态
type ConnState int32

const (
	ConnStateUnspecified          ConnState = 0
	ConnStateConnecting           ConnState = 1
	ConnStateConnected            ConnState = 2
	ConnStateAuthenticated        ConnState = 3
	ConnStateDisconnected         ConnState = 4
	ConnStateAuthenticationFailed ConnState = 5
)

func (s ConnState) String() string {
	switch s {
	case ConnStateConnecting:
		return "Connecting"
	case ConnStateConnected:
		return "Connected"
	case ConnStateAuthenticated:
		return "Authenticated"
	case ConnStateDisconnected:
		return "Disconnected"
	case ConnStateAuthenticationFailed:
		return "AuthenticationFailed"
	default:
		return "Unspecified"
	}
}

// ===============================
// 消息结构定义
// ===============================

// Message 客户端消息
type Message struct {
	RequestID  string       // 请求ID
	SequenceID uint64       // 序列号
	Type       SignalType   // 消息类型
	Body       []byte       // 消息体
	MsgID      string       // 业务消息ID
	BizType    BusinessType // 业务类型
	UserID     string       // 用户ID
	DeviceID   string       // 设备ID
	Platform   string       // 平台
}

// AuthRequest 鉴权请求参数
type AuthRequest struct {
	Token             string // 鉴权Token
	UserID            string // 用户ID
	DeviceID          string // 设备ID
	Platform          string // 平台 (web/iOS/Android)
	HeartbeatInterval uint32 // 期望心跳间隔(秒)，默认30
}

// AuthResponse 鉴权响应
type AuthResponse struct {
	Code              uint32 // 响应码，0=成功
	Msg               string // 消息
	Result            bool   // 鉴权结果
	HeartbeatInterval uint32 // 服务端心跳间隔(秒)
}

// SubscribeRequest 订阅请求参数
type SubscribeRequest struct {
	Topic string // 主题
	Qos   uint32 // 服务质量等级 (0=至多一次, 1=至少一次, 2=只有一次)
}

// BusinessMessage 业务消息
type BusinessMessage struct {
	MsgID   string       // 消息ID
	BizType BusinessType // 业务类型
	Body    []byte       // 消息体
}

// MessageHandler 消息处理函数
type MessageHandler func(msg *Message)

// ConnectHandler 连接状态处理函数
type ConnectHandler func(state ConnState, err error)

// ===============================
// 配置选项
// ===============================

// ClientOptions 客户端配置
type ClientOptions struct {
	Addr              string        // 服务器地址 (host:port)
	AuthServerAddr    string        // Auth服务地址 (host:port)
	ConnectTimeout    time.Duration // 连接超时 (默认 10s)
	ReadTimeout       time.Duration // 读取超时 (默认 30s)
	WriteTimeout      time.Duration // 写入超时 (默认 5s)
	HeartbeatInterval time.Duration // 心跳间隔 (默认 30s)
	AuthTimeout       time.Duration // 鉴权超时 (默认 10s)

	MessageHandler MessageHandler // 消息处理回调
	ConnectHandler ConnectHandler // 连接状态回调

	Debug bool
}

// Option 配置选项函数
type Option func(*ClientOptions)

// WithAddr 设置服务器地址
func WithAddr(addr string) Option {
	return func(o *ClientOptions) {
		o.Addr = addr
	}
}

// WithConnectTimeout 设置连接超时
func WithConnectTimeout(timeout time.Duration) Option {
	return func(o *ClientOptions) {
		o.ConnectTimeout = timeout
	}
}

// WithReadTimeout 设置读取超时
func WithReadTimeout(timeout time.Duration) Option {
	return func(o *ClientOptions) {
		o.ReadTimeout = timeout
	}
}

// WithWriteTimeout 设置写入超时
func WithWriteTimeout(timeout time.Duration) Option {
	return func(o *ClientOptions) {
		o.WriteTimeout = timeout
	}
}

// WithHeartbeatInterval 设置心跳间隔
func WithHeartbeatInterval(interval time.Duration) Option {
	return func(o *ClientOptions) {
		o.HeartbeatInterval = interval
	}
}

// WithAuthTimeout 设置鉴权超时
func WithAuthTimeout(timeout time.Duration) Option {
	return func(o *ClientOptions) {
		o.AuthTimeout = timeout
	}
}

// WithMessageHandler 设置消息处理回调
func WithMessageHandler(handler MessageHandler) Option {
	return func(o *ClientOptions) {
		o.MessageHandler = handler
	}
}

// WithConnectHandler 设置连接状态回调
func WithConnectHandler(handler ConnectHandler) Option {
	return func(o *ClientOptions) {
		o.ConnectHandler = handler
	}
}

// WithDebug 设置调试模式
func WithDebug(debug bool) Option {
	return func(o *ClientOptions) {
		o.Debug = debug
	}
}

// WithAuthServerAddr 设置Auth服务地址
func WithAuthServerAddr(addr string) Option {
	return func(o *ClientOptions) {
		o.AuthServerAddr = addr
	}
}

// TokenResponse Auth服务生成的Token响应
type TokenResponse struct {
	Token     string `json:"token"`     // Token
	Gateway   string `json:"gateway"`   // 网关地址
	Protocol  string `json:"protocol"`  // 协议类型
	Heartbeat int    `json:"heartbeat"` // 心跳间隔(秒)
	ExpireAt  int64  `json:"expire_at"` // 过期时间戳
}

func defaultOptions() *ClientOptions {
	return &ClientOptions{
		ConnectTimeout:    10 * time.Second,
		ReadTimeout:       50 * time.Second,
		WriteTimeout:      5 * time.Second,
		HeartbeatInterval: 20 * time.Second,
		AuthTimeout:       10 * time.Second,
	}
}

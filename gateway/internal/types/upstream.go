package types

// UpstreamRequest 上行请求，封装所有上行信息
type UpstreamRequest struct {
	ConnID string
	Msg    *Message
}

// UpstreamKind 发送器类型
type UpstreamKind int

const (
	UpstreamKindUnknown UpstreamKind = iota
	UpstreamKindKafka
	UpstreamKindGRPC
)

func (k UpstreamKind) String() string {
	switch k {
	case UpstreamKindKafka:
		return "kafka"
	case UpstreamKindGRPC:
		return "grpc"
	default:
		return "unknown"
	}
}

package types

// UpstreamJob 上行任务
type UpstreamJob struct {
    // 消息内容
    Payload MessagePayload

    // 传输层元信息
    RequestID  string
    SequenceID uint64

    // 发送方上下文
    ConnID    string
    UserID    string
    DeviceID  string
    BizType   BusinessType

    // 时间戳
    Timestamp int64

    // ACK 相关
    IsAck    bool
    AckMsgID string
}

// UpstreamRequest 上行请求
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
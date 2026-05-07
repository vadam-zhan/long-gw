package transport

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	MaxFrame = 4 << 20 // 最大Protobuf 包体 4 MiB，防大包攻击
)

// 使用 proto.MarshalOptions 减少内存分配
var marshalOpts = proto.MarshalOptions{
	UseCachedSize: true,
}

// Transport 是连接层所依赖的最小输入/输出契约。
// 其实现必须确保在并发读取和并发写入时是安全的，
// 但不同的 goroutine 可以同时进行读取和写入操作
type Transport interface {
	// Read 会阻塞，直到有完整的成帧消息可用，然后返回消息的字节数据。
	// 若连接正常关闭，则返回 (nil, io.EOF)；若出现错误，则返回 (nil, err)
	Read(ctx context.Context) ([]byte, error)

	// Write 发送一条成帧消息。实现必须确保原子性，即部分写入操作不能与其他 goroutine 的写入操作交错进行
	Write(ctx context.Context, data []byte) error

	// Close 会终止底层连接。可安全地多次调用
	Close()

	// RemoteAddr 返回对等方地址字符串（用于日志记录/速率限制）
	RemoteAddr() string

	// SetReadDeadline 为下一次 Read 调用设置一个绝对截止时间
	SetReadDeadline(t time.Time) error
}

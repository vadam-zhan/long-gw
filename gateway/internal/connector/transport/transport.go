package transport

import (
	"context"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"google.golang.org/protobuf/proto"
)

// clientSignalPool 使用 sync.Pool 复用 ClientSignal 对象，减少内存分配
var clientSignalPool = sync.Pool{
	New: func() any {
		return &gateway.ClientSignal{}
	},
}

// 使用 proto.MarshalOptions 减少内存分配
var marshalOpts = proto.MarshalOptions{
	UseCachedSize: true,
}

const (
	// 半包处理超时时间
	ConnReadTimeout = 10 * time.Second
)

// Transport 传输层接口，纯I/O操作
type Transport interface {
	// Read 读取ClientSignal
	Read(ctx context.Context) (*gateway.ClientSignal, error)

	// SetReadDeadline 设置读取超时时间
	SetReadDeadline(t time.Time) error

	// Write 写入ClientSignal数据
	Write(ctx context.Context, data *gateway.ClientSignal) error

	// Close 关闭连接
	Close() error

	// RemoteAddr 返回对端地址
	RemoteAddr() string
}

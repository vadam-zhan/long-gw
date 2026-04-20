package transport

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
)

// 使用 proto.MarshalOptions 减少内存分配
var marshalOpts = proto.MarshalOptions{
	UseCachedSize: true,
}

// Transport is the minimal I/O contract the Connection layer depends on.
// Implementations must be safe for concurrent reads and concurrent writes,
// but a read and a write may happen simultaneously from different goroutines.
type Transport interface {
	// Read blocks until a complete framed message is available and returns its bytes.
	// Returns (nil, io.EOF) on clean close, (nil, err) on error.
	Read(ctx context.Context) ([]byte, error)

	// Write sends a framed message. Implementations must ensure atomicity —
	// partial writes must not interleave with other goroutines' writes.
	Write(ctx context.Context, data []byte) error

	// Close tears down the underlying connection. Safe to call multiple times.
	Close()

	// RemoteAddr returns the peer address string (for logging / rate-limiting).
	RemoteAddr() string

	// SetReadDeadline sets an absolute deadline for the next Read call.
	// Used by the heartbeat watchdog to enforce the Ping timeout.
	SetReadDeadline(t time.Time) error
}

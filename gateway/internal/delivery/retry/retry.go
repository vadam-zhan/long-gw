package retry

import (
	"strconv"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/delivery/ack"
)

type Policy struct {
	Interval  time.Duration
	MaxRetries int
}

func DefaultPolicy() Policy {
	return Policy{
		Interval:  5 * time.Second,
		MaxRetries: 5,
	}
}

func Bump(entry *ack.PendingEntry) int {
	entry.RetryCount++
	entry.LastRetryAt = time.Now()
	if entry.Msg != nil {
		ensureRetryHeader(entry.Msg, entry.RetryCount)
	}
	return entry.RetryCount
}

func ShouldGiveUp(entry *ack.PendingEntry, policy Policy) bool {
	maxRetries := policy.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultPolicy().MaxRetries
	}
	return entry.RetryCount > maxRetries
}

func ensureRetryHeader(msg *gateway.Message, retryCount int) {
	if msg.Headers == nil {
		msg.Headers = make(map[string]string)
	}
	msg.Headers["x-retry-count"] = strconv.Itoa(retryCount)
}

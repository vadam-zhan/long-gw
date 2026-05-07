package ack

import (
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
)

type RetryTask struct {
	SessionID string
	MsgID     string
}

type PendingEntry struct {
	Msg         *gateway.Message
	RetryCount  int
	FirstSentAt time.Time
	LastRetryAt time.Time
}

func NewPendingEntry(msg *gateway.Message) *PendingEntry {
	now := time.Now()
	return &PendingEntry{
		Msg:         msg,
		FirstSentAt: now,
		LastRetryAt: now,
	}
}

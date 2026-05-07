package ack

import (
	"context"
	"log/slog"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/timer"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// Scanner 通过 Timer Wheel 做 ACK 超时检测和重试调度。
// 由 session.Registry 持有，作为全局唯一的 ACK 调度中心。
type Scanner struct {
	timer       timer.Scheduler
	ackTimeout  time.Duration // ACK 超时检测间隔
	offline     types.OfflineStore

	// sessAcks 按 session 索引的 pending ack key 集合，用于 PurgeSession
	sessAcks map[string]map[string]struct{}
	mu       sync.Mutex

	// onTimeout 由 Registry 注入，timer 回调时触发重试或离线存储
	onTimeout func(sessID, msgID string)
}

func NewScanner(t timer.Scheduler, ackTimeout time.Duration, offline types.OfflineStore) *Scanner {
	if ackTimeout <= 0 {
		ackTimeout = 5 * time.Second
	}
	return &Scanner{
		timer:      t,
		ackTimeout: ackTimeout,
		offline:    offline,
		sessAcks:   make(map[string]map[string]struct{}),
	}
}

// SetTimeoutHandler 注入超时回调，必须在 Registry 创建完成后调用。
func (s *Scanner) SetTimeoutHandler(fn func(sessID, msgID string)) {
	s.onTimeout = fn
}

// Track 注册一条 QoS-1 消息的 ACK 超时检测。
// 由 Session.Submit 在成功投递后调用。
func (s *Scanner) Track(msg *gateway.Message, sessRef types.SessionRef) {
	if msg.Delivery == nil || msg.Delivery.Qos != gateway.QosClass_AT_LEAST_ONCE {
		return
	}
	sessID := sessRef.SessionID()
	msgID := msg.MsgId
	key := ackKey(sessID, msgID)

	s.mu.Lock()
	if s.sessAcks[sessID] == nil {
		s.sessAcks[sessID] = make(map[string]struct{})
	}
	s.sessAcks[sessID][msgID] = struct{}{}
	s.mu.Unlock()

	_ = s.timer.Schedule(timer.Task{
		Key:     key,
		Type:    timer.TaskAckTimeout,
		DueAt:   time.Now().Add(s.ackTimeout),
		Payload: &timeoutPayload{sessID, msgID},
		Handler: s.handleTimeout,
	})
}

// Done 客户端已确认，取消追踪。
func (s *Scanner) Done(msgID, sessID string) {
	key := ackKey(sessID, msgID)
	s.timer.Cancel(key)

	s.mu.Lock()
	if msgs, ok := s.sessAcks[sessID]; ok {
		delete(msgs, msgID)
		if len(msgs) == 0 {
			delete(s.sessAcks, sessID)
		}
	}
	s.mu.Unlock()
}

// CancelAll Session 关闭时批量取消所有 pending timer。
func (s *Scanner) CancelAll(sessID string) {
	s.mu.Lock()
	msgs := s.sessAcks[sessID]
	delete(s.sessAcks, sessID)
	s.mu.Unlock()

	for msgID := range msgs {
		s.timer.Cancel(ackKey(sessID, msgID))
	}
}

func (s *Scanner) handleTimeout(ctx context.Context, task timer.Task) {
	if s.onTimeout == nil {
		slog.Warn("ack: timeout handler not set", "key", task.Key)
		return
	}
	p := task.Payload.(*timeoutPayload)
	s.onTimeout(p.SessID, p.MsgID)

	// 清理 sessAcks 中的记录（若重试由 Registry 重新注册则会重新添加）
	s.mu.Lock()
	if msgs, ok := s.sessAcks[p.SessID]; ok {
		delete(msgs, p.MsgID)
		if len(msgs) == 0 {
			delete(s.sessAcks, p.SessID)
		}
	}
	s.mu.Unlock()
}

type timeoutPayload struct {
	SessID string
	MsgID  string
}

func ackKey(sessID, msgID string) string {
	return "ack:" + sessID + ":" + msgID
}

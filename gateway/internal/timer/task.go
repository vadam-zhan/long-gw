package timer

import (
	"context"
	"time"
)

type TaskType int

const (
	TaskHeartbeat TaskType = iota + 1
	TaskAckTimeout
	TaskRetry
	TaskSessionSuspend
	TaskDrainKick
)

type Handler func(context.Context, Task)

// Task 是定时调度单元。
// Key 必须全局唯一，Scheduler 会用它做覆盖与取消。
type Task struct {
	Key     string
	Type    TaskType
	DueAt   time.Time
	Payload any
	Handler Handler
}

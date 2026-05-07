package timer

import "context"

type Scheduler interface {
	Start(ctx context.Context)
	Stop()
	Schedule(task Task) error
	Cancel(key string) bool
}

func NewScheduler(shardCount int) Scheduler {
	if shardCount <= 0 {
		shardCount = 64
	}
	return newShardedScheduler(shardCount)
}

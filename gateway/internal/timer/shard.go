package timer

import (
	"context"
	"hash/fnv"
	"sync"
)

type shardedScheduler struct {
	shards []*wheel
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func newShardedScheduler(shardCount int) *shardedScheduler {
	metrics := NewMetrics()
	shards := make([]*wheel, shardCount)
	for i := range shards {
		shards[i] = newWheel(metrics)
	}
	return &shardedScheduler{shards: shards}
}

func (s *shardedScheduler) Start(ctx context.Context) {
	s.once.Do(func() {
		s.ctx, s.cancel = context.WithCancel(ctx)
		for _, shard := range s.shards {
			go shard.run(s.ctx)
		}
	})
}

func (s *shardedScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *shardedScheduler) Schedule(task Task) error {
	return s.shard(task.Key).schedule(task)
}

func (s *shardedScheduler) Cancel(key string) bool {
	return s.shard(key).cancel(key)
}

func (s *shardedScheduler) shard(key string) *wheel {
	if len(s.shards) == 0 {
		return newWheel(NewMetrics())
	}
	return s.shards[hashKey(key, len(s.shards))]
}

func hashKey(key string, shardCount int) int {
	if shardCount <= 1 {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return int(hasher.Sum32() % uint32(shardCount))
}

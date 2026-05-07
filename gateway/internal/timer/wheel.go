package timer

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"time"
)

type wheel struct {
	mu      sync.Mutex
	tasks   map[string]*wheelTask
	queue   taskHeap
	wakeCh  chan struct{}
	metrics *Metrics
}

type wheelTask struct {
	Task
	cancelled bool
	index     int
}

type taskHeap []*wheelTask

func (h taskHeap) Len() int { return len(h) }
func (h taskHeap) Less(i, j int) bool {
	return h[i].DueAt.Before(h[j].DueAt)
}
func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *taskHeap) Push(x any) {
	item := x.(*wheelTask)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *taskHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

func newWheel(metrics *Metrics) *wheel {
	return &wheel{
		tasks:   make(map[string]*wheelTask),
		queue:   taskHeap{},
		wakeCh:  make(chan struct{}, 1),
		metrics: metrics,
	}
}

func (w *wheel) schedule(task Task) error {
	if task.Key == "" {
		return fmt.Errorf("timer: empty task key")
	}
	if task.Handler == nil {
		return fmt.Errorf("timer: nil task handler")
	}
	if task.DueAt.IsZero() {
		task.DueAt = time.Now()
	}

	w.mu.Lock()
	if current, ok := w.tasks[task.Key]; ok {
		current.cancelled = true
	}
	item := &wheelTask{Task: task}
	w.tasks[task.Key] = item
	heap.Push(&w.queue, item)
	if w.metrics != nil {
		w.metrics.Scheduled.Add(1)
	}
	w.mu.Unlock()

	w.signal()
	return nil
}

func (w *wheel) cancel(key string) bool {
	if key == "" {
		return false
	}

	w.mu.Lock()
	item, ok := w.tasks[key]
	if ok {
		item.cancelled = true
		delete(w.tasks, key)
		if w.metrics != nil {
			w.metrics.Cancelled.Add(1)
		}
	}
	w.mu.Unlock()

	if ok {
		w.signal()
	}
	return ok
}

func (w *wheel) signal() {
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

func (w *wheel) run(ctx context.Context) {
	for {
		next, ok := w.nextTask()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-w.wakeCh:
				continue
			}
		}

		wait := time.Until(next.DueAt)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.wakeCh:
			timer.Stop()
			continue
		case <-timer.C:
		}

		ready := w.popDue(time.Now())
		for _, item := range ready {
			if item.cancelled || item.Handler == nil {
				continue
			}
			if w.metrics != nil {
				w.metrics.Executed.Add(1)
			}
			item.Handler(ctx, item.Task)
		}
	}
}

func (w *wheel) nextTask() (*wheelTask, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(w.queue) > 0 {
		item := w.queue[0]
		if item.cancelled {
			heap.Pop(&w.queue)
			continue
		}
		return item, true
	}
	return nil, false
}

func (w *wheel) popDue(now time.Time) []*wheelTask {
	w.mu.Lock()
	defer w.mu.Unlock()

	var ready []*wheelTask
	for len(w.queue) > 0 {
		item := w.queue[0]
		if item.cancelled {
			heap.Pop(&w.queue)
			continue
		}
		if item.DueAt.After(now) {
			break
		}
		heap.Pop(&w.queue)
		if current, ok := w.tasks[item.Key]; ok && current == item {
			delete(w.tasks, item.Key)
		}
		ready = append(ready, item)
	}
	return ready
}

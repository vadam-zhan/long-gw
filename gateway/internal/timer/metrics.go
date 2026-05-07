package timer

import "sync/atomic"

type Metrics struct {
	Scheduled atomic.Int64
	Executed  atomic.Int64
	Cancelled atomic.Int64
	Overdue   atomic.Int64
}

func NewMetrics() *Metrics {
	return &Metrics{}
}

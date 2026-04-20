package pipeline

import (
	"time"
)

// Stage is a single processing step in a pipeline.
// Calling next() passes control to the next Stage.
// Returning without calling next() short-circuits the pipeline.
type Stage[C any] func(ctx C, next func())

// Chain holds an ordered list of Stages and knows how to execute them.
type Chain[C any] struct {
	stages []Stage[C]
}

func NewChain[C any](stages ...Stage[C]) Chain[C] {
	s := make([]Stage[C], len(stages))
	copy(s, stages)
	return Chain[C]{stages: s}
}

func (c Chain[C]) Append(stages ...Stage[C]) Chain[C] {
	merged := make([]Stage[C], len(c.stages)+len(stages))
	copy(merged, c.stages)
	copy(merged[len(c.stages):], stages)
	return Chain[C]{stages: merged}
}

func (c Chain[C]) Run(ctx C) {
	idx := 0
	var run func()
	run = func() {
		if idx < len(c.stages) {
			s := c.stages[idx]
			idx++
			s(ctx, run)
		}
	}
	run()
}

type BaseCtx struct {
	TraceID     string
	Aborted     bool
	AbortReason string
	ReceivedAt  time.Time
	Values      map[string]any
}

func (b *BaseCtx) Abort(reason string) {
	b.Aborted = true
	b.AbortReason = reason
}

func (b *BaseCtx) Set(key string, v any) {
	if b.Values == nil {
		b.Values = make(map[string]any)
	}
	b.Values[key] = v
}

func (b *BaseCtx) Get(key string) (any, bool) {
	v, ok := b.Values[key]
	return v, ok
}

package worker

import (
	"context"

	"github.com/vadam-zhan/long-gw/gateway/internal/worker/upstream"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// kafkaPipelineAdapter 将 connector.upstream.Sender 适配为 worker.UpstreamPipeline
type kafkaPipelineAdapter struct {
	sender upstream.Sender
}

func (a *kafkaPipelineAdapter) Send(ctx context.Context, req *types.UpstreamRequest) error {
	return a.sender.Send(ctx, req)
}

// AsUpstreamPipeline 将 connector.upstream.Sender 转换为 worker.UpstreamPipeline
func AsUpstreamPipeline(sender upstream.Sender) UpstreamPipeline {
	return &kafkaPipelineAdapter{sender: sender}
}
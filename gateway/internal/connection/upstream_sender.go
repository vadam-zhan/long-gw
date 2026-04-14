package connection

import (
	"context"

	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// SubmitResult 上行提交结果
type SubmitResult struct {
	Accepted bool
	Reason   string
}

// UpstreamSender 提交上行业务消息的接口
// 定义在 connection 包，由 worker 包实现
type UpstreamSender interface {
	Submit(ctx context.Context, job types.UpstreamJob) SubmitResult
}
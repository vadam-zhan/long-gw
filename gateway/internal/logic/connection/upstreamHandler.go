package connection

import (
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"go.uber.org/zap"
)

// UpstreamHandler 处理上行业务消息
type UpstreamHandler struct{}

func (h *UpstreamHandler) Handle(conn ConnectionAccessor, msg *types.Message) error {
	// 刷新分布式路由TTL
	if conn.IsAuthed() {
		conn.RefreshRoute()
	}

	// 提交到 worker 池
	if submitter := conn.GetUpstreamSubmitter(); submitter != nil {
		if !submitter.SubmitUpstream(msg) {
			logger.Warn("worker pool full, reject upstream job",
				zap.String("msg_id", msg.RequestID),
				zap.String("remote", conn.GetRemoteAddr()))
		}
	} else {
		logger.Debug("upstream submitter not set, skipping upstream message",
			zap.String("msg_id", msg.RequestID))
	}

	return nil
}

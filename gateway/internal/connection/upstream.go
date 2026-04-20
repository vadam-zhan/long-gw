package connection

// UpstreamHandler 上行处理器
type UpstreamHandler struct{}

// func (h *UpstreamHandler) Handle(ctx context.Context, conn ConnectionAccessor, msg *types.Message) error {
// 	// 刷新分布式路由
// 	if conn.IsAuthed() {
// 		conn.RefreshRoute()
// 	}

// 	// 提交上行业务消息
// 	if !conn.SubmitUpstream(ctx, msg).Accepted {
// 		logger.Warn("upstream submit rejected",
// 			zap.String("msg_id", msg.RequestID),
// 			zap.String("remote", conn.GetRemoteAddr()))
// 	}

// 	return nil
// }

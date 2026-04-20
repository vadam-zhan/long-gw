package handler

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/contracts"
)

// UpstreamHandler handles SignalTypeBusinessUp.
//
// This is the only Handler that delegates to a middleware pipeline (UplinkChain).
// It does not call sess.SubmitUpstream directly — that is the job of SubmitStage
// at the end of the chain, after all middleware (Validate, RateLimit, Trace, Metrics).
//
// Interaction chain:
//   UpstreamHandler.Handle(sess, conn, msg)
//     → chain.Run(sess, conn, msg)
//         → ValidateStage  : validates msg fields; error → conn.Submit(4001/4002)
//         → RateLimitStage : checks rate limit; throttled → conn.Submit(4290)
//         → TraceStage     : injects/propagates TraceID onto msg.TraceID
//         → MetricsStage   : records latency histogram (wraps next())
//         → SubmitStage    : sess.SubmitUpstream(msg)
//                              → mgr.SubmitUpstream(biz, sess, msg)
//                                → pool.upstreamCh <- UpstreamJob{Sess, Msg}
//                                  → upstreamWorker → sender.Send → Kafka
//                            on ErrPoolFull → conn.Submit(5001)

type UplinkChain interface {
	// Run executes the full uplink pipeline for one message.
	// sess and conn are both needed: sess for SubmitUpstream, conn for error replies.
	Run(sess contracts.SessionAccessor, conn *connection.Connection, msg *gateway.Message)
}

type UpstreamHandler struct {
	chain UplinkChain
}

func (h *UpstreamHandler) Handle(sess contracts.SessionAccessor, conn *connection.Connection, msg *gateway.Message) error {
	// chain.Run builds a UplinkCtx{Session: sess, Conn: conn, Message: msg}
	// and executes all stages in order.
	h.chain.Run(sess, conn, msg)
	return nil
}

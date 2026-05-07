package handler

import (
	gatewayv1 "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// LogoutHandler handles SignalTypeLogoutRequest (client-initiated logout).
//
// Interaction:
//  1. conn.Submit(resp)  — send LogoutResponse before closing.
//  2. sess.Close(kick)   — terminates the Session (Closed state, removes from registry).
//     Session.Close → conn.Close → readLoop exits → onClose.
type LogoutHandler struct{}

func (h *LogoutHandler) Handle(sess types.HandlerSession, conn types.ConnSubmitter, msg *gatewayv1.Message) error {
	resp := &gatewayv1.Message{
		Type:  gatewayv1.FrameType_KICK,
		MsgId: msg.MsgId,
		TraceContext: &gatewayv1.TraceContext{
			TraceId: msg.TraceContext.TraceId,
		},
		Body: &gatewayv1.Body{},
	}
	resp.MsgId = msg.MsgId

	// Best-effort response before closing.
	conn.Submit(resp)

	// sess.Close triggers the full teardown chain:
	// Session.Close → conn.Close(kick) → readLoop exits → Run.defer → onClose
	// → Factory.onClose: sess.DetachConn, router cleanup, registry removal
	sess.Close(&gatewayv1.KickRequest{Code: 0, Reason: "client logout"})

	return nil
}

package handler

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// LogoutHandler handles SignalTypeLogoutRequest (client-initiated logout).
//
// Interaction:
//  1. conn.Submit(resp)  — send LogoutResponse before closing.
//  2. sess.Close(kick)   — terminates the Session (Closed state, removes from registry).
//     Session.Close → conn.Close → readLoop exits → onClose.
type LogoutHandler struct{}

func (h *LogoutHandler) Handle(sess types.HandlerSession, conn types.ConnSubmitter, msg *gateway.Message) error {
	resp := &gateway.Message{
		Type:    gateway.SignalType_KICK,
		MsgId:   msg.MsgId,
		TraceId: msg.TraceId,
		Body:    &gateway.Body{},
	}
	resp.MsgId = msg.MsgId
	resp.TraceId = msg.TraceId

	// Best-effort response before closing.
	conn.Submit(resp)

	// sess.Close triggers the full teardown chain:
	// Session.Close → conn.Close(kick) → readLoop exits → Run.defer → onClose
	// → Factory.onClose: sess.DetachConn, router cleanup, registry removal
	sess.Close(&gateway.KickPayload{Code: 0, Reason: "client logout"})

	return nil
}

package handler

import (
	"fmt"
	"log/slog"
	"sync"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline/uplink"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// Architecture:
//
//	Connection.ReadLoop()
//	  └─▶ MsgHandler.Dispatch(conn, msg)
//	        ├─▶ HeartbeatHandler.Handle()  [inline, no pipeline]
//	        ├─▶ AuthHandler.Handle()        [inline, updates session state]
//	        ├─▶ AckHandler.Handle()         [inline, clears QoS retry queue]
//	        ├─▶ SubscribeHandler.Handle()   [inline, mutates router indexes]
//	        └─▶ UpstreamHandler.Handle()    [runs UplinkChain pipeline]

type Deps struct {
	types.WorkerSubmitter
	UplinkChain pipeline.Chain[*uplink.UplinkCtx]
}

type ImplRegistry struct {
	mu           sync.RWMutex
	handlers     map[gateway.SignalType]types.MsgHandler
	fallback     types.MsgHandler
	authVerifier types.AuthVerifier

	deps *Deps
}

func Build(av types.AuthVerifier, chain types.UplinkChain) *ImplRegistry {
	reg := NewRegistry(av)

	reg.Register(gateway.SignalType_PING, &HeartbeatHandler{})
	reg.Register(gateway.SignalType_ACK, &AckHandler{})
	reg.Register(gateway.SignalType_KICK, &LogoutHandler{})
	reg.Register(gateway.SignalType_BUSINESS_UP, &UpstreamHandler{chain: chain})

	return reg
}

func NewRegistry(av types.AuthVerifier) *ImplRegistry {
	return &ImplRegistry{
		handlers: map[gateway.SignalType]types.MsgHandler{},
		// fallback:     HandlerFunc(defaultFallback),
		authVerifier: av,
	}
}

func (r *ImplRegistry) Register(msgType gateway.SignalType, handler types.MsgHandler) {
	r.handlers[msgType] = handler
}

// Dispatch is the hot path called by Connection.readLoop (via Factory closure).
// O(1) map lookup → handler.Handle(sess, conn, msg).
//
// The Factory closure that calls Dispatch captures both sess and conn:
//
//	conn.Run(
//	    func(c *connection.Connection, msg *proto.Message) {
//	        reg.Dispatch(sess, c, msg)   // sess captured from outer scope
//	    },
//	    onClose,
//	)
func (r *ImplRegistry) Dispatch(sess types.HandlerSession, conn types.ConnSubmitter, msg *gateway.Message) error {
	r.mu.RLock()
	h, ok := r.handlers[msg.Type]
	r.mu.RUnlock()

	if !ok {
		r.fallback.Handle(sess, conn, msg)
		return fmt.Errorf("no handler for msg type: %v", msg.Type)
	}
	return h.Handle(sess, conn, msg)
}

func (r *ImplRegistry) AuthVerifier() types.AuthVerifier {
	return r.authVerifier
}

func defaultFallback(sess types.HandlerSession, conn types.ConnSubmitter, msg *gateway.Message) {
	slog.Warn("handler: unregistered signal type",
		"type", msg.Type,
		"mid", msg.MsgId,
		// "conn", conn.ConnID,
		// "uid", sess.UserID(),
	)
	replyError(conn, msg, 4002, fmt.Sprintf("unsupported signal type %d", msg.Type))
}

// replyError sends a structured error frame back to the client.
// Interaction: conn.Submit(errMsg) → writeCh → writeLoop → TCP.
func replyError(conn types.ConnSubmitter, ref *gateway.Message, code int32, text string) {
	errMsg := &gateway.Message{
		Type:  gateway.SignalType_ERROR,
		MsgId: ref.MsgId,
		Body: &gateway.Body{
			Payload: []byte(fmt.Sprintf(`{"code":%d,"message":"%s"}`, code, text)),
		},
	}
	// errMsg.TraceID = ref.TraceID
	// errMsg.AckID = ref.MsgID // lets client correlate error to its request
	// _ = errMsg.SetJSONBody("error", &proto.ErrorPayload{
	// 	Code:     code,
	// 	Message:  text,
	// 	RefMsgID: ref.MsgID,
	// })
	conn.Submit(errMsg) // non-blocking; drop if writeCh full
}

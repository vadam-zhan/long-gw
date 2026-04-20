package handler

import (
	"fmt"
	"log/slog"
	"sync"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/contracts"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline/uplink"
)

// Architecture:
//
//	Connection.ReadLoop()
//	  └─▶ HandlerRegistry.Dispatch(conn, msg)
//	        ├─▶ HeartbeatHandler.Handle()  [inline, no pipeline]
//	        ├─▶ AuthHandler.Handle()        [inline, updates session state]
//	        ├─▶ AckHandler.Handle()         [inline, clears QoS retry queue]
//	        ├─▶ SubscribeHandler.Handle()   [inline, mutates router indexes]
//	        └─▶ UpstreamHandler.Handle()    [runs UplinkChain pipeline]

type Handler interface {
	Handle(sess contracts.SessionAccessor, conn *connection.Connection, msg *gateway.Message) error
}

type Deps struct {
	contracts.WorkerManager
	UplinkChain pipeline.Chain[*uplink.UplinkCtx]
}

type ImplRegistry struct {
	mu           sync.RWMutex
	handlers     map[gateway.MsgType]Handler
	fallback     Handler
	authVerifier contracts.AuthVerifier

	deps *Deps
}

func Build(av contracts.AuthVerifier, chain UplinkChain) *ImplRegistry {
	reg := NewRegistry(av)

	reg.Register(gateway.MsgType_MSG_TYPE_PING, &HeartbeatHandler{})
	reg.Register(gateway.MsgType_MSG_TYPE_ACK, &AckHandler{})
	reg.Register(gateway.MsgType_MSG_TYPE_KICK, &LogoutHandler{})
	reg.Register(gateway.MsgType_MSG_TYPE_DATA, &UpstreamHandler{})

	return reg
}

func NewRegistry(av contracts.AuthVerifier) *ImplRegistry {
	return &ImplRegistry{
		handlers: map[gateway.MsgType]Handler{},
		// fallback:     HandlerFunc(defaultFallback),
		authVerifier: av,
	}
}

func (r *ImplRegistry) Register(msgType gateway.MsgType, handler Handler) {
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
func (r *ImplRegistry) Dispatch(sess contracts.SessionAccessor, conn *connection.Connection, msg *gateway.Message) error {
	r.mu.RLock()
	h, ok := r.handlers[msg.Type]
	r.mu.RUnlock()

	if !ok {
		r.fallback.Handle(sess, conn, msg)
		return fmt.Errorf("no handler for msg type: %v", msg.Type)
	}
	return h.Handle(sess, conn, msg)
}

func (r *ImplRegistry) AuthVerifier() contracts.AuthVerifier {
	return r.authVerifier
}

func defaultFallback(sess contracts.SessionAccessor, conn *connection.Connection, msg *gateway.Message) {
	slog.Warn("handler: unregistered signal type",
		"type", msg.Type,
		"mid", msg.MsgId,
		"conn", conn.ConnID,
		"uid", sess.UserID(),
	)
	replyError(conn, msg, 4002, fmt.Sprintf("unsupported signal type %d", msg.Type))
}

// replyError sends a structured error frame back to the client.
// Interaction: conn.Submit(errMsg) → writeCh → writeLoop → TCP.
func replyError(conn *connection.Connection, ref *gateway.Message, code int32, text string) {
	errMsg := &gateway.Message{
		Type:  gateway.MsgType_MSG_TYPE_ERROR,
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

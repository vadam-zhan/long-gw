package connection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/logic/codec"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type FactoryDeps struct {
	Codec        codec.Codec
	HandlerReg   types.HandlerRegistry
	SessRegistry types.SessionRegistry
	ConnRegistry types.ConnRegistry

	LocalRouter types.LocalRouterOps
	DistRouter  types.DistRouterOps

	HeartbeatTimeout time.Duration
	HandshakeTimeout time.Duration
	MaxBodySize      uint32
	SelfAddr         string // this node's address for DistRouter registration
}

type Factory struct {
	deps *FactoryDeps
}

func NewFactory(deps *FactoryDeps) *Factory {
	return &Factory{deps: deps}
}

func (f *Factory) CreateAndRun(ctx context.Context, tp transport.Transport) {
	// Phase 1: Handshake (must complete before entering the main loop).
	authInfo, authReqMsg, err := f.doHandshake(ctx, tp)
	if err != nil {
		slog.Error("factory: handshake failed", "remote", tp.RemoteAddr(), "error", err)
		f.sendRawKick(ctx, tp, 4010, err.Error())
		tp.Close()
		return
	}

	sess, isNew := f.deps.SessRegistry.GetOrCreate(authInfo.userID, authInfo.deviceID, authInfo.appID, authInfo.deviceType, authInfo.bizCode)
	if !isNew {
		slog.Info("factory: session reattached", "sid", sess.SessionID(), "uid", authInfo.userID)
	}

	// ── Step 3: Build Connection ───────────────────────────────────────────────
	// Each connection gets its own Negotiated codec instance so it can
	// independently mirror the client's chosen codec byte.
	conn := newConnection(ctx, tp, f.deps.Codec)
	conn.ConnID = authInfo.connID
	conn.UserID = authInfo.userID
	conn.AppID = authInfo.appID
	conn.DeviceID = authInfo.deviceID
	conn.DeviceType = authInfo.deviceType
	conn.BizCode = authInfo.bizCode

	// ── Step 4: Attach Connection to Session ───────────────────────────────────
	// AttachConn:
	//   - Replaces sess.conn (atomic under connMu)
	//   - Restores sess.subs → LocalRouter (JoinRoom, Subscribe)
	//   - Flushes sess.pendingAcks to the new conn (QoS-1 retry-on-reconnect)
	//   - Returns lastDeliveredSeq for offline replay
	lastSeq := sess.AttachConn(conn)

	// ── Step 5: Kick existing connection for same device ──────────────────────
	// Multi-device policy: one connection per deviceID.
	// A new login on the same device kicks the previous connection.
	kicked := f.deps.LocalRouter.RegisterSession(authInfo.userID, authInfo.deviceID, sess)
	if kicked != nil {
		kicked.Close(&gateway.KickPayload{Code: 4011, Reason: "replaced by new login"})
	}

	// ── Step 6: Register in cross-node routing ────────────────────────────────
	if err := f.deps.DistRouter.RegisterUser(ctx, authInfo.userID, authInfo.deviceID); err != nil {
		slog.Error("factory: dist router register failed", "error", err)
		// Non-fatal: local delivery still works; cross-node routing may miss this connection.
	}

	// ── Step 7: Register in connection registry ───────────────────────────────
	if err := f.deps.ConnRegistry.RegisterConn(conn.ConnID, conn); err != nil {
		slog.Error("factory: conn registry register failed", "error", err)
		sess.DetachConn()
		tp.Close()
		return
	}

	// ── Step 8: Activate connection ────────────────────────────────────────────
	// After Activate, conn.Submit accepts messages (state: Handshaking → Active).
	// Must happen before sending AuthResponse so the response goes through writeCh.
	conn.Activate()

	// ── Step 9: Send AuthResponse ──────────────────────────────────────────────
	// Includes replayFrom so the client triggers offline message replay.
	// Uses DirectWrite (synchronous, before Run starts the WriteLoop).
	if err := f.sendAuthResponse(ctx, conn, authReqMsg, authInfo, sess.SessionID(), lastSeq); err != nil {
		slog.Error("factory: send auth response failed", "error", err)
		sess.DetachConn()
		f.deps.ConnRegistry.UnregisterConn(conn.ConnID)
		tp.Close()
		return
	}

	// ── Step 10: Start HeartbeatWatchdog ──────────────────────────────────────
	go conn.HeartbeatWatchdog(f.deps.HeartbeatTimeout)

	// ── Step 11: Run (blocks until close) ────────────────────────────────────
	//
	// onMessage closure captures `sess` from this scope.
	// This is the central wiring point: Connection → HandlerRegistry → Session.
	//
	// Every inbound message flows:
	//   conn.readLoop → onMessage(conn, msg)
	//     → handlerReg.Dispatch(sess, conn, msg)
	//         → handler.Handle(sess, conn, msg)
	//
	// The closure allows Connection to remain ignorant of Session, Registry, and Pipeline.
	conn.Run(
		func(c *Connection, msg *gateway.Message) {
			// ★ The key dispatch call: connects Connection to HandlerRegistry to Session.
			// sess is captured from the enclosing CreateAndRun scope.
			// conn == c always (the closure parameter is the same *Connection).
			f.deps.HandlerReg.Dispatch(sess, c, msg)
		},
		func(c *Connection) {
			// ── Step 12: onClose cleanup ──────────────────────────────────────
			// Runs exactly once, synchronously, in the Run defer.
			f.onClose(ctx, sess, c)
		},
	)
}

func (f *Factory) onClose(ctx context.Context, sess types.FactorySession, conn *Connection) {
	// Transition Session to Suspended (conn=nil, state=Suspended, subs preserved).
	sess.DetachConn()

	// Remove from all routing indexes.
	f.deps.ConnRegistry.UnregisterConn(conn.ConnID)
	f.deps.LocalRouter.UnregisterAll(sess)

	// Note: LocalRouter.UnregisterSession is NOT called here because the Session
	// may reconnect. The user fan-out index entry persists; it will be updated
	// by the next GetOrCreate → AttachConn → RegisterSession cycle.

	// Remove from cross-node routing (DistRouter TTL = 90s, but explicit delete
	// prevents stale entries from accumulating in Redis).
	if err := f.deps.DistRouter.UnregisterUser(ctx, conn.UserID, conn.DeviceID); err != nil {
		slog.Error("factory: dist router unregister failed", "error", err)
	}

	slog.Info("factory: connection closed",
		"conn", conn.ConnID,
		"sid", sess.SessionID(),
		"uid", conn.UserID,
	)
}

func (f *Factory) sendAuthResponse(
	ctx context.Context,
	conn *Connection,
	ref *gateway.Message,
	ai *authInfo,
	sessionID string,
	lastSeq uint64,
) error {
	resp := &gateway.Message{
		Type:    gateway.SignalType_HANDSHAKE_ACK,
		MsgId:   ref.MsgId,
		TraceId: ref.TraceId,
	}

	payload := &gateway.AuthResponsePayload{
		Code:              0,
		Result:            true,
		SessionId:         sessionID,
		HeartbeatInternal: uint32(f.deps.HeartbeatTimeout.Seconds()),
		MaxBodySize:       f.deps.MaxBodySize,
		ReplayFrom:        lastSeq + 1,
	}

	pay, _ := proto.Marshal(payload)
	resp.Body = &gateway.Body{
		Type:    "auth_response",
		Payload: pay,
	}

	// DirectWrite is safe here because WriteLoop has not started.
	return conn.encodeAndWrite(resp)
}

// sendRawKick sends a Kick frame directly on tp (before Connection is created).
func (f *Factory) sendRawKick(ctx context.Context, tp transport.Transport, code int32, reason string) {
	payload := &gateway.KickPayload{
		Code:   code,
		Reason: reason,
		// ReconnectAfter: ,
	}
	p, _ := proto.Marshal(payload)
	kick := &gateway.Message{
		Type: gateway.SignalType_KICK,
		Body: &gateway.Body{
			Type:    "kick",
			Payload: p,
		},
	}

	data, _ := f.deps.Codec.Encode(kick)
	if err := tp.Write(ctx, data); err != nil {
		slog.Error("sendRawKick", "error", err)
	}
}

type authInfo struct {
	userID, deviceID, appID, deviceType, bizCode, connID string
}

// doHandshake reads the AuthRequest, validates the token, and sends AuthResponse.
// Sets ConnID, UserID, DeviceID, DeviceType on the connection.
func (f *Factory) doHandshake(ctx context.Context, tp transport.Transport) (*authInfo, *gateway.Message, error) {
	deadline := time.Now().Add(f.deps.HandshakeTimeout)
	if err := tp.SetReadDeadline(deadline); err != nil {
		return nil, nil, fmt.Errorf("set deadline: %w", err)
	}
	defer tp.SetReadDeadline(time.Time{}) // clear deadline after handshake

	raw, err := tp.Read(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("read auth request: %w", err)
	}
	msg, err := f.deps.Codec.Decode(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("decode auth request: %w", err)
	}
	if msg.Type != gateway.SignalType_HANDSHAKE {
		return nil, nil, fmt.Errorf("expected AuthRequest, got %v", msg.Type)
	}

	var authReq gateway.AuthRequestPayload
	if err := proto.Unmarshal(msg.GetBody().GetPayload(), &authReq); err != nil {
		return nil, nil, fmt.Errorf("unmarshal auth payload: %w", err)
	}
	if authReq.Token == "" {
		return nil, nil, fmt.Errorf("empty token")
	}

	// Delegate to the AuthVerifier (gRPC call to auth service or local JWT validation).
	userID, deviceID, err := f.deps.HandlerReg.AuthVerifier().Verify(authReq.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("auth verify: %w", err)
	}

	return &authInfo{
		userID:     userID,
		deviceID:   deviceID,
		appID:      authReq.AppId,
		deviceType: authReq.DeviceType,
		bizCode:    msg.BizCode,
		connID:     uuid.NewString(),
	}, msg, nil
}

func timeInN(n int) time.Time {
	return time.Now().Add(time.Second * time.Duration(n))
}

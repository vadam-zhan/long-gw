package connection

import (
	"context"
	"fmt"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/contracts"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/logic/codec"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

var zeroTime = time.Time{}

type FactorySession interface {
	contracts.SessionAccessor // embed: Ack, JoinRoom, Subscribe, SubmitUpstream, Close

	// AttachConn binds the new Connection, restores subscriptions,
	// flushes QoS-1 pending ACKs to the new conn, returns lastDeliveredSeq.
	AttachConn(conn *Connection) (lastDeliveredSeq uint64)

	// DetachConn is called on connection close: sets conn=nil, state=Suspended.
	DetachConn()
}

type SessionRegistry interface {
	// GetOrCreate returns (existing Session, false) on reconnect,
	// or (new Session, true) on first connection.
	GetOrCreate(userID, deviceID, appID, deviceType, bizCode string) (FactorySession, bool)
}

type LocalRouter interface {
	// RegisterSession registers sess in the user fan-out index and kicks any old
	// session for the same deviceID. Returns the kicked session (may be nil).
	RegisterSession(userID, deviceID string, sess FactorySession) (kicked FactorySession)

	// UnregisterSession removes sess from the user fan-out index.
	UnregisterSession(userID, deviceID string)

	// UnregisterAll removes sess from all room and topic indexes.
	// Called on connection close (subscriptionSet in Session persists for reconnect).
	UnregisterAll(sess FactorySession)
}

type DistRouter interface {
	RegisterUser(ctx context.Context, userID, deviceID, connID string) error
	UnregisterUser(ctx context.Context, userID, deviceID string) error
	Refresh(ctx context.Context, userID, deviceID, connID string) // called on Ping
}

// ConnRegistry maps connID → *Connection for direct-connection routing.
type ConnRegistry interface {
	Register(conn *Connection) error
	Unregister(conn *Connection)
}

type FactoryDeps struct {
	Codec        codec.Codec
	HandlerReg   contracts.Registry
	SessRegistry SessionRegistry
	ConnRegistry ConnRegistry

	LocalRouter LocalRouter
	DistRouter  DistRouter

	HeartbeatTimeout time.Duration
	HandshakeTimeout time.Duration
	MaxBodySize      int64
	SelfAddr         string // this node's address for DistRouter registration
}

type Factory struct {
	deps *FactoryDeps
}

func NewFactory(deps *FactoryDeps) *Factory {
	return &Factory{deps: deps}
}

func (f *Factory) CreateAndRun(ctx context.Context, tp transport.Transport) (*Connection, error) {

	// Phase 1: Handshake (must complete before entering the main loop).
	if err := f.doHandshake(ctx, tp); err != nil {
		logger.Error("factory: handshake failed", zap.String("remote", tp.RemoteAddr()), zap.Error(err))
		conn.Close(&gateway.KickPayload{Code: 4010, Reason: err.Error()})
		return nil, err
	}

	conn := NewConnection(ctx, tp, f.deps.Codec)

	// Phase 2: Register in connection registry (connID → *Connection).
	if err := f.deps.ConnRegistry.Register(conn); err != nil {
		conn.Close(&gateway.KickPayload{Code: 4010, Reason: err.Error()})
		return nil, err
	}

	return conn, nil
}

func (f *Factory) Run(ctx context.Context, conn *Connection) {
	logger.Info("factory: connection active",
		zap.String("conn", conn.ConnID),
		zap.String("uid", conn.UserID),
		zap.String("device", conn.DeviceType),
		zap.String("remote", conn.tp.RemoteAddr()),
	)

	go conn.HeartbeatWatchdog(30 * time.Second)

	conn.Run(
		func(c *Connection, msg *gateway.Message) {
			f.deps.HandlerReg.Dispatch(c, msg)
		},
		func(c *Connection) {
			f.onClose(ctx, c)
		},
	)
}

type authInfo struct {
	userID, deviceID, appID, deviceType, bizCode, connID string
}

// doHandshake reads the AuthRequest, validates the token, and sends AuthResponse.
// Sets ConnID, UserID, DeviceID, DeviceType on the connection.
func (f *Factory) doHandshake(ctx context.Context, tp transport.Transport) (authInfo, *gateway.Message, error) {
	deadline := time.Now().Add(f.deps.HandshakeTimeout)
	if err := tp.SetReadDeadline(deadline); err != nil {
		return authInfo{}, nil, fmt.Errorf("set deadline: %w", err)
	}
	defer tp.SetReadDeadline(time.Time{}) // clear deadline after handshake

	raw, err := tp.Read(ctx)
	if err != nil {
		return authInfo{}, nil, fmt.Errorf("read auth request: %w", err)
	}
	msg, err := f.deps.Codec.Decode(raw)
	if err != nil {
		return authInfo{}, nil, fmt.Errorf("decode auth request: %w", err)
	}
	if msg.Type != gateway.SignalType_Signal_Type_HANDSHAKE {
		return authInfo{}, nil, fmt.Errorf("expected AuthRequest, got %v", msg.Type)
	}

	var authReq gateway.AuthRequest
	if err := proto.Unmarshal(msg.GetBody().GetPayload(), &authReq); err != nil {
		return authInfo{}, nil, fmt.Errorf("unmarshal auth payload: %w", err)
	}
	if authReq.Token == "" {
		return authInfo{}, nil, fmt.Errorf("empty token")
	}

	// Delegate to the AuthVerifier (gRPC call to auth service or local JWT validation).
	userID, deviceID, err := f.deps.HandlerReg.AuthVerifier().Verify(authReq.Token)
	if err != nil {
		return authInfo{}, nil, fmt.Errorf("auth verify: %w", err)
	}

	return authInfo{
		userID:   userID,
		deviceID: deviceID,
		// appID:      authReq.AppId,
		// deviceType: authReq.DeviceType,
		bizCode: msg.BizCode,
	}, msg, nil
}

func timeInN(n int) time.Time {
	return time.Now().Add(time.Second * time.Duration(n))
}

// onClose is called exactly once when a connection's Run() loop exits.
func (f *Factory) onClose(ctx context.Context, conn *Connection) {
	// f.deps.ConnRegistry.Unregister(conn)
	// f.deps.LocalRouter.UnregisterUser(conn)
	// f.deps.LocalRouter.UnregisterAll(conn)
	// _ = f.deps.DistRouter.UnregisterUser(ctx, conn.UserID, conn.DeviceID)

	// log.Info("factory: connection closed",
	// "conn", conn.ConnID, "uid", conn.UserID)
}

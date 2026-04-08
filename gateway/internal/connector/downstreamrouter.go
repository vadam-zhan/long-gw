package connector

import (
	"errors"
	"sync"

	pb "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"go.uber.org/zap"
)

// DownstreamRouter 下行路由接口
type DownstreamRouterInterface interface {
	RouteDownstreamMessage(msg *pb.DownstreamKafkaMessage) error
}

var (
	ErrConnectionNotFound = errors.New("connection not found")
	ErrUserMismatch       = errors.New("user/device mismatch")
	ErrWriteChannelFull   = errors.New("write channel full")
)

// DownstreamRouter routes downstream Kafka messages to correct connections
type DownstreamRouter struct {
	connIDMap map[string]*Connection
	connMux   sync.RWMutex
}

// NewDownstreamRouter creates a new downstream router
func NewDownstreamRouter() *DownstreamRouter {
	return &DownstreamRouter{
		connIDMap: make(map[string]*Connection),
	}
}

// RegisterConnection registers a connection by its connID
func (dr *DownstreamRouter) RegisterConnection(connID string, conn *Connection) {
	dr.connMux.Lock()
	dr.connIDMap[connID] = conn
	dr.connMux.Unlock()
}

// UnregisterConnection removes a connection from the router
func (dr *DownstreamRouter) UnregisterConnection(connID string) {
	dr.connMux.Lock()
	delete(dr.connIDMap, connID)
	dr.connMux.Unlock()
}

// RouteDownstreamMessage routes a downstream message to the correct connection (implements DownstreamRouterInterface)
func (dr *DownstreamRouter) RouteDownstreamMessage(msg *pb.DownstreamKafkaMessage) error {
	dr.connMux.RLock()
	conn, ok := dr.connIDMap[msg.ConnId]
	dr.connMux.RUnlock()

	if !ok {
		logger.Warn("downstream message: connection not found",
			zap.String("conn_id", msg.ConnId),
			zap.String("device_id", msg.DeviceId),
			zap.String("correlation_id", msg.CorrelationId))
		return ErrConnectionNotFound
	}

	return dr.sendToConnection(conn, msg)
}

func (dr *DownstreamRouter) sendToConnection(conn *Connection, msg *pb.DownstreamKafkaMessage) error {
	connUserID, connDeviceID := conn.GetUserInfo()
	if connUserID != msg.UserId || connDeviceID != msg.DeviceId {
		logger.Warn("downstream message: user/device mismatch",
			zap.String("expected_user", connUserID),
			zap.String("got_user", msg.UserId),
			zap.String("expected_device", connDeviceID),
			zap.String("got_device", msg.DeviceId))
		return ErrUserMismatch
	}

	protoMsg := &types.Message{
		MsgID: msg.CorrelationId,
		// Type:     msg.TargetType,
		Body:     msg.Payload,
		UserID:   msg.UserId,
		DeviceID: msg.DeviceId,
	}

	select {
	case conn.WriteCh <- protoMsg:
		logger.Debug("downstream message routed",
			zap.String("correlation_id", msg.CorrelationId),
			zap.String("conn_id", msg.ConnId))
		return nil
	default:
		logger.Warn("downstream message: write channel full",
			zap.String("correlation_id", msg.CorrelationId),
			zap.String("conn_id", msg.ConnId))
		return ErrWriteChannelFull
	}
}

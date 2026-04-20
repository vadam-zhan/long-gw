package cmd

import (
	"context"
	"net"
	"sync/atomic"

	"github.com/soheilhy/cmux"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/handler"
	"github.com/vadam-zhan/long-gw/gateway/internal/logic/codec"
	"github.com/vadam-zhan/long-gw/gateway/internal/metrics"
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/session"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
)

type GatewayServer struct {
	ctx    context.Context
	cancel context.CancelFunc

	cmux     cmux.CMux
	listener net.Listener

	sessionRegistry *session.SessionRegistry
	workerManager   *worker.Manager
	connFactory     *connection.Factory
	localRouter     *router.LocalRouter
	distRouter      *router.DistributedRouter
	ackRetrier      *session.AckRetrier
	codec           codec.Codec

	svc *svc.ServiceContext

	// 统计
	connCount    atomic.Uint32
	sessionCount atomic.Uint32
	draining     atomic.Bool

	// handlers
	adminHandler *handler.AdminHandler
	wsHandler    *handler.WsHandler

	metricsCollector *metrics.Collector
}

// Addr returns the listen address
func (s *GatewayServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

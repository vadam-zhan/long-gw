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
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
)

type GatewayServer struct {
	ctx    context.Context
	cancel context.CancelFunc

	cmux     cmux.CMux
	listener net.Listener

	codec codec.Codec

	localRouter *router.LocalRouter
	distRouter  *router.DistributedRouter

	sessRegistry *session.Registry
	connRegistry *connection.Registry

	workerManager *worker.Manager

	connFactory *connection.Factory

	offlineStore types.OfflineStore

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

var (
	_ types.LocalRouterOps      = (*router.LocalRouter)(nil)
	_ types.SessionResolver     = (*router.LocalRouter)(nil)
	_ types.DistRouterOps       = (*router.DistributedRouter)(nil)
	_ types.WorkerSubmitter     = (*worker.Manager)(nil)
	_ types.DownstreamSubmitter = (*worker.Manager)(nil)
	_ types.HandlerSession      = (*session.Session)(nil)
	_ types.UplinkSession       = (*session.Session)(nil)
	_ types.SessionRef          = (*session.Session)(nil)
	_ types.SessionTarget       = (*session.Session)(nil)
	_ types.FactorySession      = (*session.Session)(nil)
	_ types.ConnRegistry        = (*connection.Registry)(nil)
	_ types.SessionRegistry     = (*session.Registry)(nil)
)

// downlink package also needs to use types.SessionTarget — verify:
var _ types.SessionTarget = (*session.Session)(nil)

package gateway

import (
	"context"
	"net"

	"github.com/soheilhy/cmux"
	"github.com/vadam-zhan/long-gw/internal/session"
	"github.com/vadam-zhan/long-gw/internal/svc"
)

type GatewayServer struct {
	ctx    context.Context
	cancel context.CancelFunc

	cmux     cmux.CMux
	listener net.Listener

	sessions []*session.Session

	svc *svc.ServiceContext
}

// Addr returns the listen address
func (s *GatewayServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

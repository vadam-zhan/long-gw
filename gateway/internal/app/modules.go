package app

import (
	"context"

	"github.com/gorilla/websocket"
	"github.com/soheilhy/cmux"
	"github.com/vadam-zhan/long-gw/gateway/internal/access/connruntime"
)

type Module interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type TcpModule struct {
	cmux        cmux.CMux
	connFactory *connruntime.Factory
}

type HttpModule struct {
	ctx         context.Context
	cmux        cmux.CMux
	connFactory *connruntime.Factory
	wsUpgrader  websocket.Upgrader
}

type GrpcModule struct {
	cmux cmux.CMux
}

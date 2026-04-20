package connection

import (
	"fmt"
	"sync"

	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

type Registry struct {
	mu    sync.RWMutex
	conns map[string]types.ConnSubmitter
}

func NewRegistry() *Registry {
	return &Registry{conns: make(map[string]types.ConnSubmitter)}
}

func (r *Registry) RegisterConn(connID string, conn types.ConnSubmitter) error {
	if connID == "" {
		return fmt.Errorf("registry: empty ConnID")
	}
	r.mu.Lock()
	r.conns[connID] = conn
	r.mu.Unlock()
	return nil
}

func (r *Registry) UnregisterConn(connID string) {
	r.mu.Lock()
	delete(r.conns, connID)
	r.mu.Unlock()
}

func (r *Registry) GetConn(connID string) (types.ConnSubmitter, bool) {
	r.mu.RLock()
	c, ok := r.conns[connID]
	r.mu.RUnlock()
	return c, ok
}

func (r *Registry) Count() int {
	r.mu.RLock()
	n := len(r.conns)
	r.mu.RUnlock()
	return n
}

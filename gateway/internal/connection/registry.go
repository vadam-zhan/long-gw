package connection

import (
	"fmt"
	"sync"
)

type Registry struct {
	mu    sync.RWMutex
	conns map[string]*Connection
}

func NewRegistry() *Registry {
	return &Registry{conns: make(map[string]*Connection)}
}

func (r *Registry) Register(conn *Connection) error {
	if conn.ConnID == "" {
		return fmt.Errorf("registry: empty ConnID")
	}
	r.mu.Lock()
	r.conns[conn.ConnID] = conn
	r.mu.Unlock()
	return nil
}

func (r *Registry) Unregister(conn *Connection) {
	r.mu.Lock()
	delete(r.conns, conn.ConnID)
	r.mu.Unlock()
}

func (r *Registry) Get(connID string) (*Connection, bool) {
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

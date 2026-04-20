package session

// // ConnectionRegistry Session 实现的连接注册表
// type ConnectionRegistry struct {
// 	conns map[string]worker.ConnectionWriter
// 	mux   sync.RWMutex
// }

// // NewConnectionRegistry 创建连接注册表
// func NewConnectionRegistry() *ConnectionRegistry {
// 	return &ConnectionRegistry{
// 		conns: make(map[string]worker.ConnectionWriter),
// 	}
// }

// // Register 注册连接
// func (r *ConnectionRegistry) Register(connID string, writer worker.ConnectionWriter) {
// 	r.mux.Lock()
// 	defer r.mux.Unlock()
// 	r.conns[connID] = writer
// 	logger.Debug("connection registered",
// 		zap.String("conn_id", connID))
// }

// // Unregister 注销连接
// func (r *ConnectionRegistry) Unregister(connID string) {
// 	r.mux.Lock()
// 	defer r.mux.Unlock()
// 	delete(r.conns, connID)
// 	logger.Debug("connection unregistered",
// 		zap.String("conn_id", connID))
// }

// // Get 获取连接
// func (r *ConnectionRegistry) Get(connID string) (worker.ConnectionWriter, bool) {
// 	r.mux.RLock()
// 	defer r.mux.RUnlock()
// 	w, ok := r.conns[connID]
// 	return w, ok
// }

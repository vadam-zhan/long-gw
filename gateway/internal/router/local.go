package router

import (
	"sync"

	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// LocalRouter 本地路由中心
type LocalRouter struct {
	mu sync.RWMutex

	// userConns: userID → {deviceID → *Session}
	// Used for user-level fan-out ("u:{uid}").
	userConns map[string]map[string]types.SessionTarget

	// deviceConn: deviceID → *session (single session per device)
	// Used to kick the old session when a device reconnects.
	deviceConn map[string]types.SessionTarget

	// rooms: roomID → {SessionID → *Session}
	// Used for room broadcast ("r:{roomID}").
	rooms map[string]map[string]types.SessionTarget

	// topics: topic → {SessionID → *Session}
	// Used for pub/sub ("t:{topic}") and group routing ("g:{groupID}").
	topics map[string]map[string]types.SessionTarget
}

func NewLocalRouter() *LocalRouter {
	return &LocalRouter{
		userConns:  make(map[string]map[string]types.SessionTarget),
		deviceConn: make(map[string]types.SessionTarget),
		rooms:      make(map[string]map[string]types.SessionTarget),
		topics:     make(map[string]map[string]types.SessionTarget),
	}
}

func (r *LocalRouter) RegisterSession(userID, deviceID string, sess types.SessionTarget) (kicked types.SessionTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Kick old session for same device.
	if old, ok := r.deviceConn[sess.DeviceID()]; ok && old.SessionID() == sess.SessionID() {
		kicked = old
	}
	r.deviceConn[sess.DeviceID()] = sess

	// Register in user fan-out index.
	if r.userConns[userID] == nil {
		r.userConns[userID] = make(map[string]types.SessionTarget)
	}
	r.userConns[userID][sess.DeviceID()] = sess
	return
}

func (r *LocalRouter) UnregisterSession(userID, deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.deviceConn, deviceID)
	if devs, ok := r.userConns[userID]; ok {
		delete(devs, deviceID)
		if len(devs) == 0 {
			delete(r.userConns, userID)
		}
	}
}

// JoinRoom adds conn to a room broadcast group.
func (r *LocalRouter) JoinRoom(roomID string, sess types.SessionTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rooms[roomID] == nil {
		r.rooms[roomID] = make(map[string]types.SessionTarget)
	}
	r.rooms[roomID][sess.SessionID()] = sess
}

// LeaveRoom removes conn from a room broadcast group.
func (r *LocalRouter) LeaveRoom(roomID string, sess types.SessionTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.rooms[roomID]; ok {
		delete(m, sess.SessionID())
		if len(m) == 0 {
			delete(r.rooms, roomID)
		}
	}
}

func (r *LocalRouter) Subscribe(topic string, sess types.SessionTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.topics[topic] == nil {
		r.topics[topic] = make(map[string]types.SessionTarget)
	}
	r.topics[topic][sess.SessionID()] = sess
}

func (r *LocalRouter) Unsubscribe(topic string, sess types.SessionTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.topics[topic]; ok {
		delete(m, sess.SessionID())
		if len(m) == 0 {
			delete(r.topics, topic)
		}
	}
}

// UnregisterAll 关闭时清理所有 room/topic 注册
func (r *LocalRouter) UnregisterAll(sess types.SessionTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.rooms {
		for sessionID := range v {
			if sessionID == sess.SessionID() {
				delete(v, sessionID)
			}
		}
	}
	for _, v := range r.topics {
		for sessionID := range v {
			if sessionID == sess.SessionID() {
				delete(v, sessionID)
			}
		}
	}
}

func (r *LocalRouter) Resolve(to string) ([]types.SessionTarget, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var conns []types.SessionTarget
	if m, ok := r.userConns[to]; ok {
		for _, conn := range m {
			conns = append(conns, conn)
		}
	}
	if m, ok := r.rooms[to]; ok {
		for _, conn := range m {
			conns = append(conns, conn)
		}
	}
	if m, ok := r.topics[to]; ok {
		for _, conn := range m {
			conns = append(conns, conn)
		}
	}

	if len(conns) == 0 {
		return nil, false
	}
	return conns, true
}

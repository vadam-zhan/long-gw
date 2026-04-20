package contracts

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// Handler processes one inbound message.
// Handler 层

type ConnectionAccessor interface {
	GetConnID() string
	Send(msg *gateway.Message) error
	RemoteAddr() string
}

type SessionAccessor interface {
	SessionID() string
	UserID() string
	DeviceID() string
	SubmitUpstream(msg *gateway.Message) error
	Ack(msgID string)
	JoinRoom(roomID string) error
	LeaveRoom(roomID string) error
	Close(kick *gateway.KickPayload)
}

type Registry interface {
	AuthVerifier() AuthVerifier

	Dispatch(conn ConnectionAccessor, msg *gateway.Message) error
}

// AuthVerifier
type AuthVerifier interface {
	Verify(token string) (userID, connID string, err error)
}

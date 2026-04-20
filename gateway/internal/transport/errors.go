package transport

import (
	"errors"
	"io"

	"github.com/gorilla/websocket"
)

var (
	ErrInvalidMagic    = errors.New("invalid magic byte")
	ErrMessageTooLarge = errors.New("message body too large")
	ErrInvalidHeader   = errors.New("invalid message header")
	ErrReadTimeout     = errors.New("read timeout")
	ErrWriteTimeout    = errors.New("write timeout")
)

func IsEOF(err error) bool {
	if err == nil {
		return false
	}
	return err == io.EOF || websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
	)
}

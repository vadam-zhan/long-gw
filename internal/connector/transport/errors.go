package transport

import "errors"

var (
	ErrInvalidMagic     = errors.New("invalid magic byte")
	ErrMessageTooLarge  = errors.New("message body too large")
	ErrInvalidHeader    = errors.New("invalid message header")
	ErrReadTimeout      = errors.New("read timeout")
	ErrWriteTimeout     = errors.New("write timeout")
)

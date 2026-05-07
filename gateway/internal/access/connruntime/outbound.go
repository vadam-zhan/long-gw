package connruntime

import gatewayv1 "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"

func (c *Connection) State() State { return State(c.state.Load()) }

func (c *Connection) IsActive() bool { return c.State() == StateActive }

// Activate transitions the connection from Handshaking to Active.
// Called by AuthHandler after successful token validation.
func (c *Connection) Activate() {
	c.state.CompareAndSwap(uint32(StateHandshaking), uint32(StateActive))
}

func (c *Connection) GetConnID() string {
	return c.ConnID
}
func (c *Connection) GetUserID() string {
	return c.UserID
}
func (c *Connection) GetDeviceType() string {
	return c.DeviceType
}
func (c *Connection) RemoteAddr() string {
	return c.tp.RemoteAddr()
}

func (c *Connection) Close(kick *gatewayv1.KickRequest) {

}

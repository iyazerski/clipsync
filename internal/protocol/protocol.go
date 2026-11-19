// Package protocol holds the wire limits and routes shared by all clients and the relay.
package protocol

const (
	ClipPath     = "/clip"
	EventsPath   = "/events"
	DeviceHeader = "X-Clipsync-Device"
	MaxDeviceID  = 64
	MaxText      = 256 * 1024
	MaxSealed    = (MaxText+64)*4/3 + 4
)

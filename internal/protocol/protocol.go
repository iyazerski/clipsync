package protocol

const (
	ClipPath   = "/clip"
	EventsPath = "/events"
	// PairingPath holds the short-lived mailboxes that pair a device by code.
	PairingPath  = "/pairing"
	DeviceHeader = "X-Clipsync-Device"
	MaxDeviceID  = 64
	MaxText      = 256 * 1024
	MaxSealed    = (MaxText+64)*4/3 + 4
)

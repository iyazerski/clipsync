package protocol

const (
	ClipPath   = "/clip"
	EventsPath = "/events"
	// PairingPath holds the short-lived mailboxes that pair a device by code.
	PairingPath = "/pairing"
	// DevicePath is where a device that unpairs says goodbye, with DELETE.
	DevicePath   = "/device"
	DeviceHeader = "X-Clipsync-Device"
	// ProfileHeader carries the device's sealed name and kind on its event
	// stream. A stream that has it also gets DevicesEvent events.
	ProfileHeader = "X-Clipsync-Profile"
	MaxProfile    = 1024
	// DevicesEvent is an event with Devices as its JSON data.
	DevicesEvent = "devices"
	MaxDeviceID  = 64
	MaxText      = 256 * 1024
	MaxSealed    = (MaxText+64)*4/3 + 4
)

// Devices is what the relay knows of a pairing's devices: who announced
// itself, who's connected, and who said goodbye.
type Devices struct {
	Devices []Device `json:"devices"`
	Left    []string `json:"left,omitempty"`
}

type Device struct {
	ID      string `json:"id"`
	Profile string `json:"profile"`
	Online  bool   `json:"online,omitempty"`
	// Seen is when an offline device last disconnected, in Unix milliseconds.
	Seen int64 `json:"seen,omitempty"`
}

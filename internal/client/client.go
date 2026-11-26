// Package client implements the encrypted desktop relay client.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/iyazerski/clipsync/internal/protocol"
)

const (
	requestTimeout = 20 * time.Second
	silenceLimit   = 100 * time.Second
	MinBackoff     = time.Second
	MaxBackoff     = 30 * time.Second
	StableAfter    = time.Minute
)

var ErrRefused = errors.New("the relay refused the token")

func NormalizeRelay(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return "", errors.New("the relay should be an https:// URL, like https://clip.example.com")
	}
	return raw, nil
}

func Token(secret string) (string, error) {
	k, err := derive(secret)
	return k.token, err
}

type Client struct {
	address, device string
	keys            keys
	// profile is the sealed profile that Stream announces, if any.
	profile string
}

var httpClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = requestTimeout
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}()

func New(address, secret, device string) (*Client, error) {
	k, err := derive(secret)
	if err != nil {
		return nil, err
	}
	return &Client{address: address, device: device, keys: k}, nil
}

// Announce makes Stream tell the other devices about this one, and pass on
// what the relay knows of them. Call it before Stream.
func (c *Client) Announce(p Profile) {
	c.profile = c.keys.sealProfile(c.device, p)
}

func (c *Client) request(ctx context.Context, method, path, text string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.address+path, strings.NewReader(text))
	if err != nil {
		return nil, err
	}
	for name, values := range header {
		req.Header[name] = values
	}
	req.Header.Set("Authorization", "Bearer "+c.keys.token)
	req.Header.Set(protocol.DeviceHeader, c.device)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrRefused
	}
	return nil, fmt.Errorf("the relay answered %d", resp.StatusCode)
}

func (c *Client) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := c.request(ctx, http.MethodGet, protocol.EventsPath, "", eventsHeader(uint64(time.Now().UnixMilli()), ""))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func eventsHeader(seen uint64, profile string) http.Header {
	header := http.Header{"Accept": {"text/event-stream"}, "Last-Event-Id": {strconv.FormatUint(seen, 10)}}
	if profile != "" {
		header.Set(protocol.ProfileHeader, profile)
	}
	return header
}

// Leave tells the relay that this device unpaired, so the others forget it.
func (c *Client) Leave(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := c.request(ctx, http.MethodDelete, protocol.DevicePath, "", nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Device is another device of the pairing, as the relay last knew it.
type Device struct {
	ID string
	Profile
	Online bool
	// Seen is when an offline device last disconnected.
	Seen time.Time
}

// Devices is the relay's news of the pairing's devices. Left lists the devices
// that unpaired.
type Devices struct {
	List []Device
	Left []string
}

func (c *Client) openDevices(data string) (Devices, error) {
	var wire protocol.Devices
	if err := json.Unmarshal([]byte(data), &wire); err != nil {
		return Devices{}, err
	}
	news := Devices{Left: wire.Left}
	for _, d := range wire.Devices {
		if d.ID == c.device {
			continue
		}
		p, err := c.keys.openProfile(d.ID, d.Profile)
		if err != nil {
			log.Printf("skipped device %s: %v", d.ID, err)
			continue
		}
		device := Device{ID: d.ID, Profile: p, Online: d.Online}
		if !d.Online {
			device.Seen = time.UnixMilli(d.Seen)
		}
		news.List = append(news.List, device)
	}
	return news, nil
}

func (c *Client) Publish(ctx context.Context, text string) error {
	if len(text) > protocol.MaxText {
		return errors.New("too long to send")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := c.request(ctx, http.MethodPost, protocol.ClipPath, c.keys.seal(text, time.Now()), nil)
	if err != nil {
		return err
	}
	// The relay's small response is drained so the next POST can reuse the connection.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.Body.Close()
}

// Receiver retains replay protection across reconnects. A single listener owns it.
type Receiver struct {
	opened map[[nonceSize]byte]uint64
}

type streamBody struct {
	io.Reader
	activity chan<- struct{}
}

func (b streamBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if n != 0 {
		select {
		case b.activity <- struct{}{}:
		default:
		}
	}
	return n, err
}

// Stream calls connected once headers arrive, then receive for each clip,
// including invalid clips whose event ID still advances the cursor. After
// Announce, it calls devices with each news of the other devices.
func (c *Client) Stream(ctx context.Context, receiver *Receiver, seen uint64, connected func(), receive func(uint64, string, error), devices func(Devices)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	resp, err := c.request(ctx, http.MethodGet, protocol.EventsPath, "", eventsHeader(seen, c.profile))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	connected()
	if receiver.opened == nil {
		receiver.opened = make(map[[nonceSize]byte]uint64)
	}
	activity := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(silenceLimit)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-activity:
				timer.Reset(silenceLimit)
			case <-timer.C:
				cancel(fmt.Errorf("no word from the relay for %s", silenceLimit))
				return
			}
		}
	}()
	defer func() { cancel(nil); <-done }()
	scanner := bufio.NewScanner(streamBody{Reader: resp.Body, activity: activity})
	scanner.Buffer(make([]byte, 4096), protocol.MaxSealed+16)
	var id uint64
	var event string
	var sealed strings.Builder
	for scanner.Scan() && ctx.Err() == nil {
		line := scanner.Text()
		if line == "" {
			switch {
			case sealed.Len() == 0:
			case event == protocol.DevicesEvent:
				if news, err := c.openDevices(sealed.String()); err != nil {
					log.Printf("skipped news of the devices: %v", err)
				} else if devices != nil {
					devices(news)
				}
			case event == "":
				text, err := c.keys.open(sealed.String(), time.Now(), receiver.opened)
				receive(id, text, err)
			}
			id, event = 0, ""
			sealed.Reset()
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "id":
			id = 0
			if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
				id = parsed
			}
		case "data":
			if sealed.Len()+len(value) > protocol.MaxSealed {
				return errors.New("relay event is too large")
			}
			sealed.WriteString(value)
		}
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return scanner.Err()
}

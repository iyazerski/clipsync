package main

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/iyazerski/clipsync/internal/client"
)

// follow streams c's events until the test ends, and passes on its news of the devices.
func follow(t *testing.T, c *client.Client) (news <-chan client.Devices, stop func()) {
	t.Helper()
	ch := make(chan client.Devices, 16)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Stream(ctx, &client.Receiver{}, 0, func() {}, func(uint64, string, error) {}, func(d client.Devices) { ch <- d })
	}()
	stop = func() { cancel(); <-done }
	t.Cleanup(stop)
	return ch, stop
}

// await waits for news that passes ok.
func await(t *testing.T, news <-chan client.Devices, ok func(client.Devices) bool) client.Devices {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case d := <-news:
			if ok(d) {
				return d
			}
		case <-deadline:
			t.Fatal("timed out waiting for news of the devices")
		}
	}
}

func find(d client.Devices, id string) (client.Device, bool) {
	i := slices.IndexFunc(d.List, func(dev client.Device) bool { return dev.ID == id })
	if i < 0 {
		return client.Device{}, false
	}
	return d.List[i], true
}

func TestRelayTellsDevicesAboutEachOther(t *testing.T) {
	r := newRelay("", time.Minute)
	server := httptest.NewServer(r.routes())
	t.Cleanup(server.Close)
	secret := client.RandomID(32)
	newClient := func(secret, device string, p *client.Profile) *client.Client {
		c, err := client.New(server.URL, secret, device)
		if err != nil {
			t.Fatal(err)
		}
		if p != nil {
			c.Announce(*p)
		}
		return c
	}
	mac := newClient(secret, "mac-1", &client.Profile{Name: "Ivan's MacBook", Kind: "mac"})
	phone := newClient(secret, "phone-1", &client.Profile{Name: "Pixel", Kind: "android"})
	// Older apps don't announce themselves, and get no news they couldn't read.
	old := newClient(secret, "phone-old", nil)
	stranger := newClient(client.RandomID(32), "mac-2", &client.Profile{Name: "Someone else's", Kind: "mac"})

	macNews, _ := follow(t, mac)
	follow(t, old)
	follow(t, stranger)
	_, stopPhone := follow(t, phone)

	d := await(t, macNews, func(d client.Devices) bool { _, ok := find(d, "phone-1"); return ok })
	if got, _ := find(d, "phone-1"); !got.Online || got.Name != "Pixel" || got.Kind != "android" || len(d.List) != 1 {
		t.Fatalf("devices %+v", d.List)
	}

	stopPhone()
	d = await(t, macNews, func(d client.Devices) bool { got, _ := find(d, "phone-1"); return !got.Online })
	if got, _ := find(d, "phone-1"); time.Since(got.Seen) > time.Minute {
		t.Errorf("phone last seen %v", got.Seen)
	}

	if err := phone.Leave(t.Context()); err != nil {
		t.Fatal(err)
	}
	d = await(t, macNews, func(d client.Devices) bool { return slices.Contains(d.Left, "phone-1") })
	if _, ok := find(d, "phone-1"); ok {
		t.Errorf("the phone left but is still listed: %+v", d)
	}
}

func TestRelayMakesRoomForDevicesOnline(t *testing.T) {
	r := newRelay("", time.Minute)
	r.maxMembers = 2
	server := httptest.NewServer(r.routes())
	t.Cleanup(server.Close)
	newClient := func(secret, device string) *client.Client {
		c, err := client.New(server.URL, secret, device)
		if err != nil {
			t.Fatal(err)
		}
		c.Announce(client.Profile{Name: device, Kind: "mac"})
		return c
	}
	// Goodbyes from strangers fill the relay.
	for range r.maxMembers {
		if err := newClient(client.RandomID(32), "stranger").Leave(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	secret := client.RandomID(32)
	news, _ := follow(t, newClient(secret, "mac-1"))
	follow(t, newClient(secret, "mac-2"))
	d := await(t, news, func(d client.Devices) bool { _, ok := find(d, "mac-2"); return ok })
	if got, _ := find(d, "mac-2"); !got.Online {
		t.Errorf("devices %+v", d.List)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rooms) != 1 || r.members != 2 {
		t.Errorf("%d rooms and %d members", len(r.rooms), r.members)
	}
}

func TestRelayForgetsDevicesGoneForLong(t *testing.T) {
	r := newRelay("", time.Minute)
	server := httptest.NewServer(r.routes())
	t.Cleanup(server.Close)
	c, err := client.New(server.URL, client.RandomID(32), "mac-1")
	if err != nil {
		t.Fatal(err)
	}
	c.Announce(client.Profile{Name: "Mac", Kind: "mac"})
	news, stop := follow(t, c)
	await(t, news, func(client.Devices) bool { return true })
	stop()
	// The relay notices the stream end a little later.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		r.mu.Lock()
		streams := r.streams
		r.mu.Unlock()
		if streams == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream never ended")
		}
	}

	r.forget(time.Now())
	r.mu.Lock()
	remembered := len(r.rooms)
	r.mu.Unlock()
	if remembered != 1 {
		t.Fatalf("forgot the device at once: %d rooms", remembered)
	}
	r.forget(time.Now().Add(memberTTL + time.Hour))
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rooms) != 0 || r.members != 0 {
		t.Errorf("%d rooms and %d members after a week", len(r.rooms), r.members)
	}
}

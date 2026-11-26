package main

import (
	"container/list"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/iyazerski/clipsync/internal/protocol"
)

const (
	// How long the relay remembers a device that's offline, or that left.
	memberTTL = 7 * 24 * time.Hour
	// Devices and goodbyes per pairing.
	maxRoomMembers = 16
	// At most about 1.5 KiB each, in 48 MiB.
	maxMembers = 1 << 15
)

// A member is a device that announced itself, with its sealed profile, which
// the relay can't read.
type member struct {
	profile string
	streams int
	seen    time.Time
	// idle is the member's place in relay.idle while it's offline.
	idle *list.Element
}

// An idleEntry is an offline device, or a goodbye.
type idleEntry struct {
	key    roomKey
	device string
	at     time.Time
}

func validProfile(profile string) bool {
	return profile != "" && len(profile) <= protocol.MaxProfile && strings.IndexFunc(profile, notBase64URL) < 0
}

// join counts a stream of device, which announced profile, and returns the
// member that part takes, or nil if there's no room for it. The caller holds r.mu.
func (r *relay) join(rm *room, device, profile string) *member {
	if el := rm.left[device]; el != nil {
		r.drop(el)
	}
	m := rm.members[device]
	if m == nil {
		if !r.makeRoom(rm) {
			return nil
		}
		m = &member{}
		rm.members[device] = m
		r.members++
	} else if m.idle != nil {
		r.idle.Remove(m.idle)
		m.idle = nil
	}
	m.profile = profile
	m.streams++
	rm.devicesChanged()
	return m
}

// part ends a stream that join counted as m. The caller holds r.mu.
func (r *relay) part(key roomKey, rm *room, device string, m *member, now time.Time) {
	// The device may have left, and come back as another member, since.
	if m == nil || rm.members[device] != m {
		return
	}
	m.streams--
	if m.streams == 0 {
		m.seen = now
		m.idle = r.idle.PushBack(&idleEntry{key: key, device: device, at: now})
		rm.devicesChanged()
	}
}

// makeRoom frees a place in rm and in the relay for one more member or
// goodbye, forgetting the longest idle ones, and reports whether it could.
// Devices online always find room: there are fewer streams than places.
// The caller holds r.mu.
func (r *relay) makeRoom(rm *room) bool {
	if len(rm.members)+len(rm.left) >= maxRoomMembers {
		var oldest *list.Element
		for _, el := range rm.left {
			oldest = older(oldest, el)
		}
		for _, m := range rm.members {
			if m.idle != nil {
				oldest = older(oldest, m.idle)
			}
		}
		if oldest == nil {
			return false
		}
		r.drop(oldest)
	}
	if r.members >= r.maxMembers {
		front := r.idle.Front()
		if front == nil {
			return false
		}
		if key, other := r.drop(front); other != rm {
			r.prune(key, other)
		}
	}
	return true
}

func older(a, b *list.Element) *list.Element {
	if a == nil || b.Value.(*idleEntry).at.Before(a.Value.(*idleEntry).at) {
		return b
	}
	return a
}

// drop forgets the idle device or goodbye at el, and returns its room, for
// the caller to prune. The caller holds r.mu.
func (r *relay) drop(el *list.Element) (roomKey, *room) {
	e := r.idle.Remove(el).(*idleEntry)
	rm := r.rooms[e.key]
	if rm.left[e.device] == el {
		delete(rm.left, e.device)
	} else {
		delete(rm.members, e.device)
	}
	r.members--
	return e.key, rm
}

// forget drops the devices and goodbyes idle for longer than memberTTL, and
// the rooms that they leave empty.
func (r *relay) forget(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for el := r.idle.Front(); el != nil && now.Sub(el.Value.(*idleEntry).at) > memberTTL; el = r.idle.Front() {
		r.prune(r.drop(el))
	}
}

func (r *relay) forgetOldMembers(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.forget(now)
		}
	}
}

// devicesChanged wakes the streams that follow rm's devices. The caller holds r.mu.
func (rm *room) devicesChanged() {
	for l := range rm.listeners {
		if l.devices != nil {
			select {
			case l.devices <- struct{}{}:
			default:
			}
		}
	}
}

// devicesEvent is rm's devices as event data. The caller holds r.mu.
func (rm *room) devicesEvent() string {
	list := protocol.Devices{Devices: []protocol.Device{}}
	for _, id := range slices.Sorted(maps.Keys(rm.members)) {
		m := rm.members[id]
		d := protocol.Device{ID: id, Profile: m.profile, Online: m.streams > 0}
		if !d.Online {
			d.Seen = m.seen.UnixMilli()
		}
		list.Devices = append(list.Devices, d)
	}
	list.Left = slices.Sorted(maps.Keys(rm.left))
	data, _ := json.Marshal(list)
	return string(data)
}

// leave forgets the device, which unpaired, and tells the others.
func (r *relay) leave(w http.ResponseWriter, _ *http.Request, key roomKey, device string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rm := r.room(key)
	if m := rm.members[device]; m != nil {
		if m.idle != nil {
			r.idle.Remove(m.idle)
		}
		delete(rm.members, device)
		r.members--
	}
	if rm.left[device] == nil && r.makeRoom(rm) {
		rm.left[device] = r.idle.PushBack(&idleEntry{key: key, device: device, at: time.Now()})
		r.members++
	}
	rm.devicesChanged()
	r.prune(key, rm)
	w.WriteHeader(http.StatusNoContent)
}

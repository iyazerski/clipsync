package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/iyazerski/clipsync/internal/client"
)

// forgetAfter is how long the list keeps a device that hasn't connected.
const forgetAfter = 30 * 24 * time.Hour

// knownDevice is another device of the pairing, as this one last heard of it.
// The relay forgets devices when it restarts, so the app keeps them.
type knownDevice struct {
	Name string    `json:"name"`
	Kind string    `json:"kind"`
	Seen time.Time `json:"seen"`
	// Pending marks a device of the pairing that this computer left for a
	// new one, until it joins the new one too.
	Pending bool `json:"pending,omitempty"`
}

// listedDevice is a row of the device list.
type listedDevice struct {
	knownDevice
	id     string
	online bool
}

// devicesPath keeps the list next to the config, apart from what the CLI wrote.
func devicesPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "devices.json")
}

func loadDevices(path string) map[string]knownDevice {
	known := map[string]knownDevice{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return known
	}
	if err == nil {
		err = json.Unmarshal(data, &known)
	}
	if err != nil {
		log.Printf("reading the device list: %v", err)
	}
	return known
}

func saveDevices(path string, known map[string]knownDevice) {
	var err error
	if len(known) == 0 {
		err = os.Remove(path)
	} else {
		err = writeJSON(path, known)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("saving the device list: %v", err)
	}
}

// devicesNews takes in what the relay knows of the other devices.
func (a *app) devicesNews(news client.Devices) {
	now := time.Now()
	for _, id := range news.Left {
		delete(a.known, id)
	}
	a.online = map[string]bool{}
	for _, d := range news.List {
		seen := d.Seen
		if d.Online {
			seen = now
			a.online[d.ID] = true
		}
		a.known[d.ID] = knownDevice{Name: d.Name, Kind: d.Kind, Seen: seen}
	}
	maps.DeleteFunc(a.known, func(_ string, d knownDevice) bool { return !d.Pending && now.Sub(d.Seen) > forgetAfter })
	saveDevices(a.devicesPath, a.known)
}

// lostTouch keeps when the devices that were online were last seen, as this
// device can no longer tell.
func (a *app) lostTouch() {
	if len(a.online) == 0 {
		return
	}
	now := time.Now()
	for id := range a.online {
		d := a.known[id]
		d.Seen = now
		a.known[id] = d
	}
	a.online = nil
	saveDevices(a.devicesPath, a.known)
}

func (a *app) forgetDevice(id string) {
	delete(a.known, id)
	saveDevices(a.devicesPath, a.known)
}

func (a *app) forgetDevices() {
	a.known, a.online = map[string]knownDevice{}, nil
	saveDevices(a.devicesPath, a.known)
}

// otherDevices lists the online devices first, then the most recently seen.
func (a *app) otherDevices() []listedDevice {
	list := make([]listedDevice, 0, len(a.known))
	for id, d := range a.known {
		list = append(list, listedDevice{knownDevice: d, id: id, online: a.online[id]})
	}
	slices.SortFunc(list, func(x, y listedDevice) int {
		if x.online != y.online {
			if x.online {
				return -1
			}
			return 1
		}
		return cmp.Or(y.Seen.Compare(x.Seen), cmp.Compare(x.Name, y.Name), cmp.Compare(x.id, y.id))
	})
	return list
}

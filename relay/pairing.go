package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/iyazerski/clipsync/internal/protocol"
)

const (
	pairingTTL = 10 * time.Minute
	// Pairing takes one session per device being added, for a few minutes.
	maxPairings = 1024
	// The largest message of the exchange: the inviter's tag and the sealed pairing.
	maxPairingMessage = 1024
	// Below the clients' 20-second wait for a response.
	pairingWait = 15 * time.Second
)

// A pairing is a mailbox of three write-once slots, for the exchange that pairs
// a device by code: a, the inviter's share; b, the joiner's answer; and c, the
// inviter's sealed reply. Only the inviter, which holds owner, can write c or
// close it. Writing b once means one guess of the code per pairing.
type pairing struct {
	owner   string
	slots   map[string]string
	changed chan struct{}
	expiry  *time.Timer
}

// setSlot fills an empty slot and wakes its readers. The caller holds r.mu.
func (p *pairing) setSlot(name, value string) {
	p.slots[name] = value
	close(p.changed)
	p.changed = make(chan struct{})
}

func (r *relay) pairingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+protocol.PairingPath, r.authorize(r.openPairing))
	mux.HandleFunc("GET "+protocol.PairingPath+"/{nameplate}/{slot}", r.readSlot)
	mux.HandleFunc("PUT "+protocol.PairingPath+"/{nameplate}/{slot}", r.writeSlot)
	mux.HandleFunc("DELETE "+protocol.PairingPath+"/{nameplate}", r.closePairing)
}

func readMessage(w http.ResponseWriter, req *http.Request) (string, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxPairingMessage))
	if err != nil || len(body) == 0 || strings.IndexFunc(string(body), notBase64URL) >= 0 {
		w.WriteHeader(http.StatusBadRequest)
		return "", false
	}
	return string(body), true
}

// openPairing starts a pairing with the inviter's share and answers its
// nameplate, the public part of the code, and the key that owns it.
func (r *relay) openPairing(w http.ResponseWriter, req *http.Request, _ roomKey, _ string) {
	share, ok := readMessage(w, req)
	if !ok {
		return
	}
	owner := make([]byte, 32)
	rand.Read(owner)
	p := &pairing{owner: base64.RawURLEncoding.EncodeToString(owner), slots: map[string]string{"a": share}, changed: make(chan struct{})}

	r.mu.Lock()
	nameplate := r.freeNameplate()
	if nameplate == "" {
		r.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	r.pairings[nameplate] = p
	p.expiry = time.AfterFunc(pairingTTL, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.dropPairing(nameplate, p)
	})
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Nameplate string `json:"nameplate"`
		Owner     string `json:"owner"`
	}{nameplate, p.owner})
}

// freeNameplate picks an unused nameplate, as short as the relay's load
// allows: 2 digits, then 3, then 4. The caller holds r.mu.
func (r *relay) freeNameplate() string {
	if len(r.pairings) >= r.maxPairings {
		return ""
	}
	for low := int64(10); low < 10000; low *= 10 {
		for range 8 {
			n, _ := rand.Int(rand.Reader, big.NewInt(low*9))
			nameplate := strconv.FormatInt(low+n.Int64(), 10)
			if r.pairings[nameplate] == nil {
				return nameplate
			}
		}
	}
	return ""
}

// dropPairing forgets p, waking its readers. The caller holds r.mu.
func (r *relay) dropPairing(nameplate string, p *pairing) {
	if r.pairings[nameplate] == p {
		delete(r.pairings, nameplate)
		p.expiry.Stop()
		close(p.changed)
	}
}

func validSlot(name string) bool { return name == "a" || name == "b" || name == "c" }

// readSlot answers a slot's message, waiting a little for it. 204 asks the
// client to try again; 404 means the pairing ended or never was.
func (r *relay) readSlot(w http.ResponseWriter, req *http.Request) {
	nameplate, slot := req.PathValue("nameplate"), req.PathValue("slot")
	if !validSlot(slot) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	timer := time.NewTimer(pairingWait)
	defer timer.Stop()
	r.mu.Lock()
	p := r.pairings[nameplate]
	r.mu.Unlock()
	for {
		r.mu.Lock()
		// A new pairing may take the nameplate of one that ended while this waited.
		current := r.pairings[nameplate] == p
		var value string
		var changed chan struct{}
		if p != nil {
			value, changed = p.slots[slot], p.changed
		}
		r.mu.Unlock()
		switch {
		case p == nil || !current:
			w.WriteHeader(http.StatusNotFound)
			return
		case value != "":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, value)
			return
		}
		select {
		case <-changed:
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-req.Context().Done():
			return
		}
	}
}

func (p *pairing) ownedBy(req *http.Request) bool {
	key, _ := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(key), []byte(p.owner)) == 1
}

// writeSlot fills b, which anyone with the nameplate may, or c, which only the
// inviter may. A filled slot answers 409.
func (r *relay) writeSlot(w http.ResponseWriter, req *http.Request) {
	nameplate, slot := req.PathValue("nameplate"), req.PathValue("slot")
	if slot != "b" && slot != "c" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	value, ok := readMessage(w, req)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pairings[nameplate]
	switch {
	case p == nil:
		w.WriteHeader(http.StatusNotFound)
	case slot == "c" && !p.ownedBy(req):
		w.WriteHeader(http.StatusUnauthorized)
	case p.slots[slot] != "":
		w.WriteHeader(http.StatusConflict)
	default:
		p.setSlot(slot, value)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (r *relay) closePairing(w http.ResponseWriter, req *http.Request) {
	nameplate := req.PathValue("nameplate")
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pairings[nameplate]
	switch {
	case p == nil:
		w.WriteHeader(http.StatusNotFound)
	case !p.ownedBy(req):
		w.WriteHeader(http.StatusUnauthorized)
	default:
		r.dropPairing(nameplate, p)
		w.WriteHeader(http.StatusNoContent)
	}
}

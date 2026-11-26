package syncer_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iyazerski/clipsync/internal/client"
	"github.com/iyazerski/clipsync/internal/syncer"
)

//go:embed testdata/pairing.txt
var pairingFixture string

type memoryBoard struct {
	mu      sync.Mutex
	count   uint64
	text    string
	written chan string
}

func (b *memoryBoard) ChangeCount() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

func (b *memoryBoard) Read() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text, nil
}

func (b *memoryBoard) Write(text string) (uint64, error) {
	n := b.copy(text)
	b.written <- text
	return n, nil
}

func (b *memoryBoard) copy(text string) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text = text
	b.count++
	return b.count
}

func receiveWithin[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sync")
		var zero T
		return zero
	}
}

func TestSyncWithExistingWireProtocol(t *testing.T) {
	fixtureSecret, fixtureToken, _ := strings.Cut(strings.TrimSpace(pairingFixture), "\n")
	// This key is the HMAC-SHA256 of "clipsync/key" under the shared test secret.
	const fixtureKey = "sT5EE_r8G18kn9sXlmhAOBGU9LmyXv4wzl712Qf0Dtg"
	key, err := base64.RawURLEncoding.DecodeString(fixtureKey)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(text string, nonce byte) string {
		packet := make([]byte, 13)
		packet[0], packet[1] = 2, nonce
		plain := binary.BigEndian.AppendUint64(nil, uint64(time.Now().UnixMilli()))
		packet = gcm.Seal(packet, packet[1:], append(plain, text...), nil)
		return base64.RawURLEncoding.EncodeToString(packet)
	}
	publish := make(chan string, 8)
	events := make(chan string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fixtureToken || r.Header.Get("X-Clipsync-Device") != "go-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /events":
			if r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Last-Event-ID") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			for id := 1; ; id++ {
				select {
				case <-r.Context().Done():
					return
				case data := <-events:
					fmt.Fprintf(w, "id: %d\ndata: %s\n\n", id, data)
					flusher.Flush()
				}
			}
		case "POST /clip":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			publish <- string(body)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	relay, err := client.New(server.URL, fixtureSecret, "go-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	board := &memoryBoard{written: make(chan string, 8)}
	board.copy("clipboard contents from before startup")
	stopped := make(chan struct{})
	reported := make(chan syncer.Event, 8)
	go func() {
		defer close(stopped)
		syncer.Run(ctx, relay, board, func(e syncer.Event, _ error) { reported <- e })
	}()
	defer func() {
		stop()
		receiveWithin(t, stopped)
	}()

	stream := func(sealed string) {
		t.Helper()
		select {
		case events <- sealed:
		case <-time.After(5 * time.Second):
			t.Fatal("relay not streaming")
		}
	}
	const in = "receive 🔐"
	sealed := encode(strings.Repeat("x", 256*1024-len(in))+in, 1)
	stream(sealed)
	if got := receiveWithin(t, board.written); len(got) != 256*1024 || !strings.HasSuffix(got, in) {
		t.Fatalf("wrong incoming clip: %d bytes", len(got))
	}
	if got := []syncer.Event{receiveWithin(t, reported), receiveWithin(t, reported)}; got[0] != syncer.Connected || got[1] != syncer.Received {
		t.Fatalf("reported %v, want connected then received", got)
	}

	const out = "send UTF-8 🧪"
	board.copy(out)
	wire := receiveWithin(t, publish)
	packet, err := base64.RawURLEncoding.DecodeString(wire)
	if err != nil || len(packet) < 13 || packet[0] != 2 {
		t.Fatalf("invalid wire clip: %v", err)
	}
	plain, err := gcm.Open(nil, packet[1:13], packet[13:], nil)
	if err != nil || len(plain) < 8 || string(plain[8:]) != out {
		t.Fatalf("wrong outgoing clip or unwanted echo: %v", err)
	}
	if got := receiveWithin(t, reported); got != syncer.Sent {
		t.Fatalf("reported %v after sending", got)
	}

	// Replaying the remote clip after the clipboard changed must not restore it.
	stream(sealed)
	stream(encode("fresh clip", 2))
	if got := receiveWithin(t, board.written); got != "fresh clip" {
		t.Fatalf("replay replaced the clipboard with %d bytes", len(got))
	}
}

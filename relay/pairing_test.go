package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iyazerski/clipsync/internal/client"
)

func pairingRelay(t *testing.T) (*relay, string, *client.Client) {
	t.Helper()
	r := newRelay("", time.Minute)
	server := httptest.NewServer(r.routes())
	t.Cleanup(server.Close)
	inviter, err := client.New(server.URL, client.RandomID(32), "inviter")
	if err != nil {
		t.Fatal(err)
	}
	return r, server.URL, inviter
}

func TestPairingByCode(t *testing.T) {
	_, address, inviter := pairingRelay(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	inv, err := inviter.Invite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := client.Pairing{Relay: "https://clip.example.com", Secret: client.RandomID(32)}
	finished := make(chan error, 1)
	go func() { finished <- inv.Finish(ctx, want) }()

	got, err := client.Join(ctx, address, client.FormatCode(inv.Code()))
	if err != nil || got != want {
		t.Fatalf("joined %+v, %v; want %+v", got, err, want)
	}
	if err := <-finished; err != nil {
		t.Fatalf("finish: %v", err)
	}
	if _, err := client.Join(ctx, address, inv.Code()); !errors.Is(err, client.ErrCodeGone) {
		t.Errorf("joining twice: %v", err)
	}
}

func TestPairingRefusesAWrongCode(t *testing.T) {
	_, address, inviter := pairingRelay(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	inv, err := inviter.Invite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- inv.Finish(ctx, client.Pairing{Relay: "https://clip.example.com", Secret: client.RandomID(32)})
	}()

	code := []byte(inv.Code())
	last := len(code) - 1
	code[last] = '0' + (code[last]-'0'+1)%10
	if _, err := client.Join(ctx, address, string(code)); !errors.Is(err, client.ErrWrongCode) {
		t.Errorf("joiner: %v", err)
	}
	if err := <-finished; !errors.Is(err, client.ErrWrongCode) {
		t.Errorf("inviter: %v", err)
	}
	// One guess per code: the right code no longer works either.
	if _, err := client.Join(ctx, address, inv.Code()); !errors.Is(err, client.ErrCodeGone) {
		t.Errorf("joining after a wrong code: %v", err)
	}
}

func TestPairingGuardsItsSlots(t *testing.T) {
	r, address, inviter := pairingRelay(t)
	r.maxPairings = 1
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	inv, err := inviter.Invite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inviter.Invite(ctx); err == nil {
		t.Error("opened a pairing past the limit")
	}
	nameplate := inv.Code()[:len(inv.Code())-6]
	for _, tc := range []struct {
		slot   string
		status int
	}{
		{"a", http.StatusNotFound},
		{"c", http.StatusUnauthorized},
		{"b", http.StatusNoContent},
		{"b", http.StatusConflict},
	} {
		req, _ := http.NewRequestWithContext(ctx, "PUT", address+"/pairing/"+nameplate+"/"+tc.slot, strings.NewReader("abc"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("PUT %s = %d, want %d", tc.slot, resp.StatusCode, tc.status)
		}
	}
}

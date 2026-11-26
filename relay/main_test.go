package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var (
	testToken  = strings.Repeat("a", tokenSize)
	otherToken = strings.Repeat("b", tokenSize)
)

func request(t *testing.T, ctx context.Context, address, method, path, token, device, body, seen string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, address+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Clipsync-Device", device)
	req.Header.Set("Last-Event-ID", seen)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func publishClip(t *testing.T, ctx context.Context, address, token, device, sealed string) string {
	t.Helper()
	resp := request(t, ctx, address, "POST", "/clip", token, device, sealed, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish returned %d", resp.StatusCode)
	}
	var result struct{ ID uint64 }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.ID == 0 {
		t.Fatalf("invalid publish response: %v", err)
	}
	return fmt.Sprint(result.ID)
}

func eventStream(t *testing.T, ctx context.Context, address, token, device, seen string) *bufio.Reader {
	t.Helper()
	resp := request(t, ctx, address, "GET", "/events", token, device, "", seen)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("events returned %d (%s, buffering=%q)", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("X-Accel-Buffering"))
	}
	return bufio.NewReader(resp.Body)
}

func wantEvent(t *testing.T, stream *bufio.Reader, id, sealed string) {
	t.Helper()
	for _, want := range []string{"id: " + id + "\n", "data: " + sealed + "\n", "\n"} {
		if got, err := stream.ReadString('\n'); got != want || err != nil {
			t.Fatalf("event = %q (%v), want %q", got, err, want)
		}
	}
}

func TestRelayDeliversToPeersAndCatchesUp(t *testing.T) {
	server := httptest.NewServer(newRelay("", time.Minute).routes())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	a := eventStream(t, ctx, server.URL, testToken, "a", "")
	id1 := publishClip(t, ctx, server.URL, testToken, "a", "first")
	b := eventStream(t, ctx, server.URL, testToken, "b", "")
	wantEvent(t, b, id1, "first")
	id2 := publishClip(t, ctx, server.URL, testToken, "b", "second")
	wantEvent(t, a, id2, "second")
	a = eventStream(t, ctx, server.URL, testToken, "a", id2)
	id3 := publishClip(t, ctx, server.URL, testToken, "b", "third")
	wantEvent(t, a, id3, "third")
}

func TestRelayKeepsPairingsApart(t *testing.T) {
	server := httptest.NewServer(newRelay("", time.Minute).routes())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	publishClip(t, ctx, server.URL, testToken, "a", "mine")
	other := eventStream(t, ctx, server.URL, otherToken, "c", "")
	publishClip(t, ctx, server.URL, testToken, "a", "mine-again")
	id := publishClip(t, ctx, server.URL, otherToken, "d", "theirs")
	wantEvent(t, other, id, "theirs")
}

func TestRelayRefusesWorkBeyondItsLimits(t *testing.T) {
	relay := newRelay("", time.Minute)
	relay.maxStreams, relay.maxRoomStreams, relay.maxStored = 4, 2, 10
	server := httptest.NewServer(relay.routes())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	eventStream(t, ctx, server.URL, testToken, "a", "")
	eventStream(t, ctx, server.URL, testToken, "b", "")
	if resp := request(t, ctx, server.URL, "GET", "/events", testToken, "x", "", ""); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("full room returned %d", resp.StatusCode)
	}
	publishClip(t, ctx, server.URL, testToken, "a", "123456")
	// A newer clip replaces the room's stored one, so it counts only once.
	publishClip(t, ctx, server.URL, testToken, "a", "1234567")

	// Past the storage budget, a clip still reaches connected devices, but isn't kept.
	c := eventStream(t, ctx, server.URL, otherToken, "c", "")
	id := publishClip(t, ctx, server.URL, otherToken, "e", "1234")
	wantEvent(t, c, id, "1234")
	d := eventStream(t, ctx, server.URL, otherToken, "d", "")
	id = publishClip(t, ctx, server.URL, otherToken, "e", "12")
	wantEvent(t, d, id, "12")

	if resp := request(t, ctx, server.URL, "GET", "/events", strings.Repeat("c", tokenSize), "f", "", ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("full relay returned %d", resp.StatusCode)
	}
}

func TestRelayForgetsExpiredClips(t *testing.T) {
	const ttl = 25 * time.Millisecond
	server := httptest.NewServer(newRelay("", ttl).routes())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	publishClip(t, ctx, server.URL, testToken, "a", "expired")
	time.Sleep(4 * ttl)
	b := eventStream(t, ctx, server.URL, testToken, "b", "")
	id := publishClip(t, ctx, server.URL, testToken, "a", "fresh")
	wantEvent(t, b, id, "fresh")
}

func TestRelayRejectsBadRequests(t *testing.T) {
	server := httptest.NewServer(newRelay(testToken, time.Minute).routes())
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name, method, path, token, device, body string
		status                                  int
	}{
		{"malformed token", "GET", "/events", "wrong", "a", "", http.StatusUnauthorized},
		{"other token on a private relay", "GET", "/events", otherToken, "a", "", http.StatusUnauthorized},
		{"device", "GET", "/events", testToken, "", "", http.StatusBadRequest},
		{"alphabet", "POST", "/clip", testToken, "a", "foo\nbar", http.StatusBadRequest},
		{"limit", "POST", "/clip", testToken, "a", strings.Repeat("x", 360_000), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := request(t, t.Context(), server.URL, tc.method, tc.path, tc.token, tc.device, tc.body, "")
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
		})
	}
}

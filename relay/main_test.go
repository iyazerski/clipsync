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

const testToken = "test-token"

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

func publishClip(t *testing.T, ctx context.Context, address, device, sealed string) string {
	t.Helper()
	resp := request(t, ctx, address, "POST", "/clip", testToken, device, sealed, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish returned %d", resp.StatusCode)
	}
	var result struct{ ID uint64 }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.ID == 0 {
		t.Fatalf("invalid publish response: %v", err)
	}
	return fmt.Sprint(result.ID)
}

func eventStream(t *testing.T, ctx context.Context, address, device, seen string) *bufio.Reader {
	t.Helper()
	resp := request(t, ctx, address, "GET", "/events", testToken, device, "", seen)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events returned %d (%s)", resp.StatusCode, resp.Header.Get("Content-Type"))
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
	server := httptest.NewServer(newRelay(testToken, time.Minute).routes())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	a := eventStream(t, ctx, server.URL, "a", "")
	id1 := publishClip(t, ctx, server.URL, "a", "first")
	b := eventStream(t, ctx, server.URL, "b", "")
	wantEvent(t, b, id1, "first")
	id2 := publishClip(t, ctx, server.URL, "b", "second")
	wantEvent(t, a, id2, "second")
	bAgain := eventStream(t, ctx, server.URL, "b", id1)
	id3 := publishClip(t, ctx, server.URL, "a", "third")
	wantEvent(t, bAgain, id3, "third")
	c := eventStream(t, ctx, server.URL, "c", id3)
	id4 := publishClip(t, ctx, server.URL, "a", "fourth")
	wantEvent(t, c, id4, "fourth")
}

func TestRelayForgetsExpiredClips(t *testing.T) {
	const ttl = 25 * time.Millisecond
	server := httptest.NewServer(newRelay(testToken, ttl).routes())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	publishClip(t, ctx, server.URL, "a", "expired")
	time.Sleep(4 * ttl)
	b := eventStream(t, ctx, server.URL, "b", "")
	id := publishClip(t, ctx, server.URL, "a", "fresh")
	wantEvent(t, b, id, "fresh")
}

func TestRelayRejectsBadRequests(t *testing.T) {
	server := httptest.NewServer(newRelay(testToken, time.Minute).routes())
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name, method, path, token, device, body string
		status                                  int
	}{
		{"token", "GET", "/events", "wrong", "a", "", http.StatusUnauthorized},
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

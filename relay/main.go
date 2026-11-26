package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/iyazerski/clipsync/internal/protocol"
)

const (
	keepAlive    = 45 * time.Second
	writeTimeout = 30 * time.Second
	queueSize    = 16
	// Sized for about 1,000 pairings of a few devices each in 256 MiB.
	maxStreams = 4096
	// A few devices per pairing, plus streams that a device dropped but the relay
	// hasn't noticed yet.
	maxRoomStreams = 8
	maxStored      = 32 << 20
	// Devices send base64url(HMAC-SHA256(secret, "clipsync/token")).
	tokenSize = 43
)

var version = "0.1.0"

//go:embed assets/pair.html
var pairPage []byte

type clip struct {
	id             uint64
	device, sealed string
}

// A room holds the devices of one pairing, found by the hash of their shared token.
type room struct {
	latest    *clip
	expiry    *time.Timer
	listeners map[chan *clip]struct{}
}

type roomKey = [sha256.Size]byte

type relay struct {
	// private, when set, is the only token's key that the relay lets in.
	private *roomKey
	ttl     time.Duration

	maxStreams, maxRoomStreams, maxStored int

	mu      sync.Mutex
	id      uint64
	streams int
	stored  int
	rooms   map[roomKey]*room
}

func newRelay(token string, ttl time.Duration) *relay {
	r := &relay{
		ttl:            ttl,
		maxStreams:     maxStreams,
		maxRoomStreams: maxRoomStreams,
		maxStored:      maxStored,
		rooms:          make(map[roomKey]*room),
	}
	if token != "" {
		key := roomKey(sha256.Sum256([]byte(token)))
		r.private = &key
	}
	return r
}

func notBase64URL(c rune) bool {
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_')
}

func validToken(token string) bool {
	return len(token) == tokenSize && strings.IndexFunc(token, notBase64URL) < 0
}

// room returns the room for key, making it if needed. The caller holds r.mu.
func (r *relay) room(key roomKey) *room {
	rm := r.rooms[key]
	if rm == nil {
		rm = &room{listeners: make(map[chan *clip]struct{})}
		r.rooms[key] = rm
	}
	return rm
}

// prune forgets a room with no devices and no clip. The caller holds r.mu.
func (r *relay) prune(key roomKey, rm *room) {
	if rm.latest == nil && len(rm.listeners) == 0 && r.rooms[key] == rm {
		delete(r.rooms, key)
	}
}

func (r *relay) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /pair", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(pairPage)
	})
	mux.HandleFunc("POST "+protocol.ClipPath, r.authorize(r.publish))
	mux.HandleFunc("GET "+protocol.EventsPath, r.authorize(r.events))
	return mux
}

func (r *relay) authorize(next func(http.ResponseWriter, *http.Request, roomKey, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		key := roomKey(sha256.Sum256([]byte(token)))
		if !ok || !validToken(token) || r.private != nil && subtle.ConstantTimeCompare(key[:], r.private[:]) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		device := req.Header.Get(protocol.DeviceHeader)
		if device == "" || len(device) > protocol.MaxDeviceID || strings.IndexFunc(device, func(c rune) bool { return c < ' ' || c > '~' }) >= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		next(w, req, key, device)
	}
}

func (r *relay) publish(w http.ResponseWriter, req *http.Request, key roomKey, device string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, protocol.MaxSealed))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	if len(body) == 0 || bytes.IndexFunc(body, notBase64URL) >= 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	rm := r.room(key)
	r.id = max(r.id+1, uint64(time.Now().UnixMilli()))
	item := &clip{id: r.id, device: device, sealed: string(body)}
	if rm.latest != nil {
		r.stored -= len(rm.latest.sealed)
		rm.expiry.Stop()
		rm.latest, rm.expiry = nil, nil
	}
	// When the relay holds all the clips it can, it still passes this one on, but
	// devices that connect later won't get it.
	if r.stored+len(body) <= r.maxStored {
		r.stored += len(body)
		rm.latest = item
		rm.expiry = time.AfterFunc(r.ttl, func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if rm.latest == item {
				r.stored -= len(item.sealed)
				rm.latest, rm.expiry = nil, nil
				r.prune(key, rm)
			}
		})
	}
	for listener := range rm.listeners {
		select {
		case listener <- item:
		default:
			// Don't let a slow device block publishers. Catch it up to the latest clip.
			for range len(listener) {
				select {
				case <-listener:
				default:
				}
			}
			listener <- item
		}
	}
	r.prune(key, rm)
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		ID uint64 `json:"id"`
	}{ID: item.id})
}

func (r *relay) events(w http.ResponseWriter, req *http.Request, key roomKey, device string) {
	var seen uint64
	if id, err := strconv.ParseUint(req.Header.Get("Last-Event-ID"), 10, 64); err == nil {
		seen = id
	}
	listener := make(chan *clip, queueSize)
	r.mu.Lock()
	if r.streams >= r.maxStreams {
		r.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	rm := r.room(key)
	if len(rm.listeners) >= r.maxRoomStreams {
		r.mu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	r.streams++
	rm.listeners[listener] = struct{}{}
	if rm.latest != nil {
		listener <- rm.latest
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.streams--
		delete(rm.listeners, listener)
		r.prune(key, rm)
		r.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// A device that stops reading would otherwise hold its stream and clips forever.
	controller := http.NewResponseController(w)
	send := func(parts ...string) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(writeTimeout))
		for _, part := range parts {
			if _, err := io.WriteString(w, part); err != nil {
				return false
			}
		}
		return controller.Flush() == nil
	}
	if !send() {
		return
	}

	ticker := time.NewTicker(keepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ticker.C:
			if !send(":\n\n") {
				return
			}
		case item := <-listener:
			if item.id <= seen || item.device == device {
				continue
			}
			seen = item.id
			if !send("id: ", strconv.FormatUint(item.id, 10), "\ndata: ", item.sealed, "\n\n") {
				return
			}
		}
	}
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func serve(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("clipsync-relay", flag.ContinueOnError)
	bind := flags.String("bind", env("BIND_ADDR", "0.0.0.0:8080"), "address to bind (BIND_ADDR)")
	token := flags.String("token", "", "the only token to let in, for a private relay (CLIPSYNC_TOKEN)")
	public := flags.Bool("public", os.Getenv("CLIPSYNC_PUBLIC") == "true", "let every pairing in, instead of only CLIPSYNC_TOKEN (CLIPSYNC_PUBLIC=true)")
	ttl := flags.String("clip-ttl-secs", env("CLIP_TTL_SECS", "120"), "seconds to retain the latest clip (CLIP_TTL_SECS)")
	showVersion := flags.Bool("version", false, "print version")
	flags.BoolVar(showVersion, "V", false, "print version")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Println("clipsync-relay", version)
		return nil
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	// Read the token after flag setup so help never prints its value.
	if *token == "" {
		*token = os.Getenv("CLIPSYNC_TOKEN")
	}
	switch {
	case *public && *token != "":
		return errors.New("set either CLIPSYNC_TOKEN or CLIPSYNC_PUBLIC=true, not both")
	case !*public && *token == "":
		return errors.New("set CLIPSYNC_TOKEN to the relay token in clipsync's settings, or CLIPSYNC_PUBLIC=true to let every pairing in")
	case !*public && !validToken(*token):
		return errors.New("CLIPSYNC_TOKEN should be the relay token in clipsync's settings")
	}
	limit, err := strconv.ParseUint(*ttl, 10, 64)
	if err != nil || limit > uint64(math.MaxInt64/int64(time.Second)) {
		return errors.New("invalid clip TTL in seconds")
	}
	if _, err := netip.ParseAddrPort(*bind); err != nil {
		return fmt.Errorf("invalid bind address: %w", err)
	}
	listener, err := net.Listen("tcp", *bind)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           newRelay(*token, time.Duration(limit)*time.Second).routes(),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    8 << 10,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	shutdownDone := make(chan struct{})
	stopped := context.AfterFunc(ctx, func() {
		defer close(shutdownDone)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(shutdownCtx) != nil {
			_ = server.Close()
		}
	})
	if *public {
		log.Printf("listening on %s, open to every pairing", listener.Addr())
	} else {
		log.Printf("listening on %s, letting in only CLIPSYNC_TOKEN", listener.Addr())
	}
	err = server.Serve(listener)
	if !stopped() {
		<-shutdownDone
	}
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

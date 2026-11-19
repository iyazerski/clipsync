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
	keepAlive = 45 * time.Second
	queueSize = 16
)

var version = "0.1.0"

//go:embed assets/pair.html
var pairPage []byte

type clip struct {
	id             uint64
	device, sealed string
}

type relay struct {
	token [sha256.Size]byte
	ttl   time.Duration

	mu        sync.Mutex
	id        uint64
	latest    *clip
	expiry    *time.Timer
	listeners map[chan *clip]struct{}
}

func newRelay(token string, ttl time.Duration) *relay {
	return &relay{token: sha256.Sum256([]byte(token)), ttl: ttl, listeners: make(map[chan *clip]struct{})}
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

func (r *relay) authorize(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(token))
		if !ok || subtle.ConstantTimeCompare(got[:], r.token[:]) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		device := req.Header.Get(protocol.DeviceHeader)
		if device == "" || len(device) > protocol.MaxDeviceID || strings.IndexFunc(device, func(c rune) bool { return c < ' ' || c > '~' }) >= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		next(w, req, device)
	}
}

func (r *relay) publish(w http.ResponseWriter, req *http.Request, device string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, protocol.MaxSealed))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	if len(body) == 0 || bytes.IndexFunc(body, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_')
	}) >= 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	r.id = max(r.id+1, uint64(time.Now().UnixMilli()))
	item := &clip{id: r.id, device: device, sealed: string(body)}
	r.latest = item
	if r.expiry != nil {
		r.expiry.Stop()
	}
	r.expiry = time.AfterFunc(r.ttl, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.latest == item {
			r.latest = nil
		}
	})
	for listener := range r.listeners {
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
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		ID uint64 `json:"id"`
	}{ID: item.id})
}

func (r *relay) events(w http.ResponseWriter, req *http.Request, device string) {
	var seen uint64
	if id, err := strconv.ParseUint(req.Header.Get("Last-Event-ID"), 10, 64); err == nil {
		seen = id
	}
	listener := make(chan *clip, queueSize)
	r.mu.Lock()
	r.listeners[listener] = struct{}{}
	if r.latest != nil {
		listener <- r.latest
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.listeners, listener)
		r.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flush := http.NewResponseController(w).Flush
	if flush() != nil {
		return
	}

	ticker := time.NewTicker(keepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ticker.C:
			if _, err := io.WriteString(w, ":\n\n"); err != nil || flush() != nil {
				return
			}
		case item := <-listener:
			if item.id <= seen || item.device == device {
				continue
			}
			seen = item.id
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", item.id, item.sealed); err != nil || flush() != nil {
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
	token := flags.String("token", "", "token every device sends (CLIPSYNC_TOKEN)")
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
	if *token == "" {
		return errors.New("set CLIPSYNC_TOKEN to the output of clipsync token")
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
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	stopped := context.AfterFunc(ctx, func() { server.Close() })
	defer stopped()
	log.Printf("listening on %s", listener.Addr())
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
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

// Package syncer coordinates clipboard sync between the desktop and the relay.
package syncer

import (
	"context"
	"crypto/sha256"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/iyazerski/clipsync/internal/client"
	"github.com/iyazerski/clipsync/internal/protocol"
)

const (
	pollInterval = 300 * time.Millisecond
	wakeGap      = 5 * time.Second
	sendFor      = 120 * time.Second
)

// Event is what the syncer tells the app about, for its status.
type Event int

const (
	Connected Event = iota
	// Disconnected comes with the error that ended the stream, or nil.
	Disconnected
	Sent
	Received
)

// Report is called from the syncer's goroutines.
type Report func(Event, error)

type Board interface {
	ChangeCount() uint64
	Read() (string, error)
	Write(string) (uint64, error)
}

type clipboard struct {
	mu      sync.Mutex
	board   Board
	count   uint64
	text    [sha256.Size]byte
	hasText bool
}

func (c *clipboard) copied() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := c.board.ChangeCount()
	if count == c.count {
		return ""
	}
	text, err := c.board.Read()
	if err != nil {
		log.Print(err)
		return ""
	}
	c.count = count
	if len(text) > protocol.MaxText {
		log.Printf("not sending a copy of %d KB: the limit is %d KB", len(text)/1024, protocol.MaxText/1024)
	}
	if len(text) > protocol.MaxText || strings.TrimSpace(text) == "" {
		c.hasText = false
		return ""
	}
	id := sha256.Sum256([]byte(text))
	if c.hasText && c.text == id {
		return ""
	}
	c.text, c.hasText = id, true
	return text
}

func (c *clipboard) holds(id [sha256.Size]byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasText && c.text == id
}

func (c *clipboard) receive(ctx context.Context, text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A clip that arrives as the sync stops must not land after it stopped.
	if ctx.Err() != nil {
		return false
	}
	id := sha256.Sum256([]byte(text))
	if c.hasText && c.text == id {
		return false
	}
	count, err := c.board.Write(text)
	if err != nil {
		log.Printf("skipped a clip: %v", err)
		return false
	}
	c.count, c.text, c.hasText = count, id, true
	log.Printf("received %d characters", utf8.RuneCountInString(text))
	return true
}

// Run syncs board through relay until ctx ends. devices, if not nil, gets the
// news of the other devices, for a relay client that announced itself.
func Run(ctx context.Context, relay *client.Client, board Board, report Report, devices func(client.Devices)) {
	c := &clipboard{board: board, count: board.ChangeCount()}
	pending := make(chan string, 1)
	woke := make(chan struct{}, 1)
	var workers sync.WaitGroup
	workers.Go(func() { poll(ctx, c, pending, woke) })
	workers.Go(func() { send(ctx, relay, c, pending, report) })
	workers.Go(func() { listen(ctx, relay, c, woke, report, devices) })
	workers.Wait()
}

func poll(ctx context.Context, c *clipboard, pending chan string, woke chan<- struct{}) {
	wall := time.Now().UnixMilli()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now().UnixMilli()
		// The monotonic clock on macOS doesn't count time asleep.
		if now-wall > wakeGap.Milliseconds() {
			select {
			case woke <- struct{}{}:
			default:
			}
		}
		wall = now
		if text := c.copied(); text != "" {
			select {
			case <-pending:
			default:
			}
			pending <- text
		}
	}
}

func send(ctx context.Context, relay *client.Client, c *clipboard, pending <-chan string, report Report) {
	var text string
	for ctx.Err() == nil {
		if text == "" {
			select {
			case <-ctx.Done():
				return
			case text = <-pending:
			}
		}

		id := sha256.Sum256([]byte(text))
		deadline := time.Now().UnixMilli() + sendFor.Milliseconds()
		backoff := client.MinBackoff
		var next string
		for ctx.Err() == nil && c.holds(id) {
			// Let an in-flight POST finish: canceling it could let an older clip arrive after a newer one.
			if err := relay.Publish(ctx, text); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("sending failed: %v", err)
			} else {
				log.Printf("sent %d characters", utf8.RuneCountInString(text))
				report(Sent, nil)
				break
			}
			if time.Now().UnixMilli() > deadline {
				log.Printf("gave up sending a copy after %s", sendFor)
				break
			}
			wait := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
			case next = <-pending:
			case <-wait.C:
			}
			wait.Stop()
			if next != "" {
				break
			}
			backoff = min(backoff*2, client.MaxBackoff)
		}
		text = next
	}
}

func listen(ctx context.Context, relay *client.Client, c *clipboard, woke <-chan struct{}, report Report, devices func(client.Devices)) {
	seen := uint64(time.Now().UnixMilli())
	var receiver client.Receiver
	backoff := client.MinBackoff
	for ctx.Err() == nil {
		started := time.Now()
		streamCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			connected := func() {
				log.Print("connected")
				report(Connected, nil)
			}
			done <- relay.Stream(streamCtx, &receiver, seen, connected, func(id uint64, text string, err error) {
				seen = max(seen, id)
				if err != nil {
					log.Printf("skipped a clip: %v", err)
				} else if c.receive(ctx, text) {
					report(Received, nil)
				}
			}, devices)
		}()
		select {
		case <-woke:
			log.Print("woke from sleep; reconnecting")
			cancel()
			<-done
			backoff = client.MinBackoff
		case err := <-done:
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.Printf("relay: %v", err)
			}
			report(Disconnected, err)
			if time.Since(started) > client.StableAfter {
				backoff = client.MinBackoff
			}
			wait := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
			case <-woke:
				backoff = client.MinBackoff
			case <-wait.C:
				backoff = min(backoff*2, client.MaxBackoff)
			}
			wait.Stop()
		}
	}
}

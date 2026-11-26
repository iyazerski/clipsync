package main

import (
	"errors"
	"log"
	"runtime"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/iyazerski/clipsync/internal/client"
	"github.com/iyazerski/clipsync/internal/syncer"
)

type page int

const (
	pageWelcome page = iota
	pageJoin
	// pageReady follows setting up the first device, to add the others.
	pageReady
	pageDevices
	pageSettings
)

type syncState int

const (
	syncPaused syncState = iota
	syncConnecting
	syncConnected
	syncOffline
	syncRefused
	// syncBroken means this computer's clipboard can't be used.
	syncBroken
)

// desktop is what the app needs from the system around it.
type desktop interface {
	// startSync syncs cfg's pairing until stop is called. report runs on the main thread.
	startSync(cfg config, report syncer.Report) (stop func(), err error)
	// copySecret puts text on the clipboard marked as private, so that
	// clipboard managers and clipsync itself leave it alone.
	copySecret(text string)
	openAtLogin() bool
	setOpenAtLogin(on bool) error
	showTray(on bool)
	// language is the language the user chose, "" while the app follows the system's.
	language() string
	// languages are the languages clipsync is written in.
	languages() []string
	setLanguage(tag string)
	// checkUpdates, installUpdate and restart move app.updates along, on the main thread.
	checkUpdates(manual bool)
	installUpdate()
	restart()
	openLogs()
	// changed tells the system UI, as the tray, that the app's state changed.
	changed()
}

type updateStage int

const (
	updateIdle updateStage = iota
	updateChecking
	updateCurrent
	updateAvailable
	updateInstalling
	// updateReady means the new version runs once the app restarts.
	updateReady
	updateFailed
)

type updates struct {
	enabled bool
	stage   updateStage
	version string
}

// app is the state of the window and the tray. It's read and changed on the main thread.
type app struct {
	sys      desktop
	path     string
	cfg      config
	computer string
	page     page

	sync     syncState
	stopSync func()
	// syncs counts the syncs started, to tell a stopped one's late reports apart.
	syncs        int
	lastSent     time.Time
	lastReceived time.Time

	link       string
	linkFailed bool

	relayOpen   bool
	relayDraft  string
	relayFailed bool

	adding   bool
	addTab   int
	showLink bool
	qr       [][]bool

	unpairing bool
	updates   updates
}

func newApp(sys desktop, path string, cfg config, paused bool) *app {
	a := &app{sys: sys, path: path, cfg: cfg, computer: computerName(), page: pageWelcome}
	if cfg.paired() {
		a.page = pageDevices
		a.pairingChanged()
		if !paused {
			a.resume()
		}
	}
	return a
}

func (a *app) save() error {
	if err := saveConfig(a.path, a.cfg); err != nil {
		log.Printf("saving settings: %v", err)
		return err
	}
	return nil
}

func (a *app) pairingChanged() {
	a.showLink = false
	a.qr = nil
	if q, err := qrcode.New(a.cfg.pairLink(), qrcode.Low); err == nil {
		q.DisableBorder = true
		a.qr = q.Bitmap()
	}
}

// pair starts syncing with a new pairing, then shows next.
func (a *app) pair(relay, secret string, next page) error {
	a.pause()
	cfg := a.cfg
	cfg.Relay, cfg.Secret = relay, secret
	if cfg.Device == "" {
		prefix := "mac"
		if runtime.GOOS == "windows" {
			prefix = "win"
		}
		cfg.Device = prefix + "-" + client.RandomID(9)
	}
	previous := a.cfg
	a.cfg = cfg
	if err := a.save(); err != nil {
		a.cfg = previous
		if previous.paired() {
			a.resume()
		}
		return err
	}
	if err := a.sys.setOpenAtLogin(true); err != nil {
		log.Printf("opening at login: %v", err)
	}
	a.pairingChanged()
	a.page = next
	a.resume()
	return nil
}

func (a *app) setUpFirst() error {
	return a.pair(a.cfg.Relay, client.RandomID(32), pageReady)
}

func (a *app) join() error {
	relay, secret, err := parsePairLink(a.link)
	if err != nil {
		a.linkFailed = true
		return nil
	}
	if err := a.pair(relay, secret, pageDevices); err != nil {
		return err
	}
	a.link, a.linkFailed = "", false
	return nil
}

func (a *app) unpair() error {
	cfg := a.cfg
	cfg.Secret = ""
	if err := saveConfig(a.path, cfg); err != nil {
		log.Printf("saving settings: %v", err)
		return err
	}
	a.cfg = cfg
	a.page, a.adding, a.qr = pageWelcome, false, nil
	a.pause()
	return nil
}

// setRelay switches to another relay, keeping the secret.
func (a *app) setRelay(raw string) error {
	relay, err := client.NormalizeRelay(raw)
	if err != nil {
		a.relayFailed = true
		return nil
	}
	a.relayOpen, a.relayFailed = false, false
	if relay == a.cfg.Relay {
		return nil
	}
	previous := a.cfg.Relay
	a.cfg.Relay = relay
	if a.cfg.paired() {
		if err := a.save(); err != nil {
			a.cfg.Relay = previous
			return err
		}
		a.pairingChanged()
		if a.sync != syncPaused {
			a.pause()
			a.resume()
		}
	}
	return nil
}

func (a *app) pause() {
	if a.stopSync != nil {
		a.stopSync()
		a.stopSync = nil
	}
	a.sync = syncPaused
	a.sys.changed()
}

func (a *app) resume() {
	if a.stopSync != nil {
		return
	}
	a.syncs++
	syncs := a.syncs
	stop, err := a.sys.startSync(a.cfg, func(e syncer.Event, err error) {
		if a.syncs == syncs && a.stopSync != nil {
			a.report(e, err)
		}
	})
	if err != nil {
		log.Printf("can't sync: %v", err)
		a.sync = syncBroken
	} else {
		a.sync, a.stopSync = syncConnecting, stop
	}
	a.sys.changed()
}

func (a *app) report(e syncer.Event, err error) {
	switch e {
	case syncer.Connected:
		a.sync = syncConnected
	case syncer.Disconnected:
		a.sync = syncOffline
		if errors.Is(err, client.ErrRefused) {
			a.sync = syncRefused
		}
	case syncer.Sent:
		a.lastSent = time.Now()
	case syncer.Received:
		a.lastReceived = time.Now()
	}
	a.sys.changed()
}

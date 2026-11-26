package main

import (
	"errors"
	"log"
	"maps"
	"runtime"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/iyazerski/clipsync/desktop/locales"
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
	// startSync syncs cfg's pairing until stop is called. report and devices
	// run on the main thread.
	startSync(cfg config, report syncer.Report, devices func(client.Devices)) (stop func(), err error)
	// leave tells the other devices of cfg's pairing that this one unpaired.
	leave(cfg config)
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
	// invite lets other devices join cfg's pairing by code, one after another,
	// until stop is called or a device tries a wrong code. report runs on the
	// main thread, unless stop was called.
	invite(cfg config, report func(inviteEvent)) (stop func())
	// joinCode pairs with the device that shows code. done runs on the main
	// thread, unless stop was called.
	joinCode(relay, code string, done func(client.Pairing, error)) (stop func())
}

// inviteEvent is news of the invitations: a new code, or what became of the last.
type inviteEvent struct {
	code   string
	joined bool
	wrong  bool
	// wrong and err end the invitations.
	err error
}

type codeNotice int

const (
	codeNoNotice codeNotice = iota
	codeJoined
	// codeWrong and codeFailed stop the invitations until the user asks for a
	// new code, so that nobody can keep guessing.
	codeWrong
	codeFailed
)

type joinError int

const (
	joinNoError joinError = iota
	joinInvalid
	joinWrongCode
	joinCodeGone
	joinOffline
)

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

	devicesPath string
	known       map[string]knownDevice
	// online holds the devices that the relay last said were connected.
	online map[string]bool

	link     string
	joining  bool
	stopJoin func()
	joinErr  joinError

	// code is what other devices can join with, "" while one is being made.
	code       string
	codeNotice codeNotice
	stopInvite func()

	// toast is a message to show once.
	toast string

	relayOpen   bool
	relayDraft  string
	relayFailed bool

	adding   bool
	addTab   int
	showLink bool
	qr       [][]bool

	unpairing bool
	// removing is the device that the user may remove, while asked.
	removing     listedDevice
	askingRemove bool
	updates      updates
}

func newApp(sys desktop, path string, cfg config, paused bool) *app {
	a := &app{sys: sys, path: path, cfg: cfg, computer: computerName(), page: pageWelcome, devicesPath: devicesPath(path)}
	a.known = loadDevices(a.devicesPath)
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
	syncing := a.stopSync != nil
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
		if syncing {
			a.resume()
		}
		return err
	}
	// Only the first pairing, as removing a device makes a new one too.
	if !previous.paired() {
		if err := a.sys.setOpenAtLogin(true); err != nil {
			log.Printf("opening at login: %v", err)
		}
	}
	a.pairingChanged()
	a.closeInvite()
	a.forgetDevices()
	a.page = next
	a.resume()
	return nil
}

func (a *app) setUpFirst() error {
	return a.pair(a.cfg.Relay, client.RandomID(32), pageReady)
}

// removeDevice moves this computer to a new pairing, which the device can't
// join, as it knows the old secret but not the new one. The other devices
// stay listed, to be added again with a code.
func (a *app) removeDevice(id string) error {
	previous, kept := a.cfg, maps.Clone(a.known)
	delete(kept, id)
	if err := a.pair(a.cfg.Relay, client.RandomID(32), pageDevices); err != nil {
		return err
	}
	a.sys.leave(previous)
	for id, d := range kept {
		d.Pending = true
		kept[id] = d
	}
	a.known = kept
	saveDevices(a.devicesPath, a.known)
	a.toast = locales.Removed(a.removing.Name)
	a.adding = true
	return nil
}

// join pairs with what the user entered: a pairing link, at once, or a code,
// through the relay.
func (a *app) join() error {
	a.joinErr = joinNoError
	if relay, secret, err := parsePairLink(a.link); err == nil {
		return a.joined(relay, secret)
	}
	code, ok := client.ParseCode(a.link)
	if !ok {
		a.joinErr = joinInvalid
		return nil
	}
	a.joining = true
	a.stopJoin = a.sys.joinCode(a.cfg.Relay, code, func(p client.Pairing, err error) {
		a.joining, a.stopJoin = false, nil
		switch {
		case err == nil:
			if err := a.joined(p.Relay, p.Secret); err != nil {
				a.toast = locales.SaveFailed()
			}
		case errors.Is(err, client.ErrWrongCode):
			a.joinErr = joinWrongCode
		case errors.Is(err, client.ErrCodeGone):
			a.joinErr = joinCodeGone
		default:
			log.Printf("joining: %v", err)
			a.joinErr = joinOffline
		}
	})
	return nil
}

func (a *app) cancelJoin() {
	if a.stopJoin != nil {
		a.stopJoin()
	}
	a.joining, a.stopJoin = false, nil
}

func (a *app) joined(relay, secret string) error {
	if err := a.pair(relay, secret, pageDevices); err != nil {
		return err
	}
	a.link, a.toast = "", locales.Joined()
	return nil
}

// keepInvite keeps a code ready while the app shows how to add a device.
func (a *app) keepInvite() {
	want := a.cfg.paired() && (a.adding || a.page == pageReady)
	stopped := a.codeNotice == codeWrong || a.codeNotice == codeFailed
	switch {
	case want && a.stopInvite == nil && !stopped:
		a.openInvite()
	case !want && (a.stopInvite != nil || a.codeNotice != codeNoNotice):
		a.closeInvite()
	}
}

// openInvite makes a new code, and another after each device that joins.
func (a *app) openInvite() {
	a.closeInvite()
	a.stopInvite = a.sys.invite(a.cfg, func(e inviteEvent) {
		switch {
		case e.err != nil:
			log.Printf("making a code: %v", e.err)
			a.closeInvite()
			a.codeNotice = codeFailed
		case e.wrong:
			a.closeInvite()
			a.codeNotice = codeWrong
		case e.joined:
			a.code, a.codeNotice = "", codeJoined
		default:
			a.code = e.code
		}
	})
}

// hidden ends what only makes sense while the window shows, so that no code
// stays open behind it.
func (a *app) hidden() {
	a.adding = false
	a.closeInvite()
}

func (a *app) closeInvite() {
	if a.stopInvite != nil {
		a.stopInvite()
	}
	a.stopInvite, a.code, a.codeNotice = nil, "", codeNoNotice
}

func (a *app) unpair() error {
	cfg := a.cfg
	cfg.Secret = ""
	if err := saveConfig(a.path, cfg); err != nil {
		log.Printf("saving settings: %v", err)
		return err
	}
	a.sys.leave(a.cfg)
	a.cfg = cfg
	a.page, a.adding, a.qr = pageWelcome, false, nil
	a.closeInvite()
	a.pause()
	a.forgetDevices()
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
		a.closeInvite()
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
	a.lostTouch()
	a.sync = syncPaused
	a.sys.changed()
}

func (a *app) resume() {
	if a.stopSync != nil {
		return
	}
	a.syncs++
	syncs := a.syncs
	current := func() bool { return a.syncs == syncs && a.stopSync != nil }
	stop, err := a.sys.startSync(a.cfg, func(e syncer.Event, err error) {
		if current() {
			a.report(e, err)
		}
	}, func(news client.Devices) {
		if current() {
			a.devicesNews(news)
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
		a.lostTouch()
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

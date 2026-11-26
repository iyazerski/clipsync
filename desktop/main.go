// clipsync syncs the clipboard of this computer with the user's other devices,
// and lives in the menu bar (macOS) or the notification area (Windows).
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/iyazerski/clipsync/desktop/locales"
	"github.com/iyazerski/clipsync/internal/client"
	"github.com/iyazerski/clipsync/internal/syncer"
)

// backgroundArg starts the app without its window, as after an update.
// pausedArg keeps a paused sync paused across a restart.
const (
	backgroundArg = "--background"
	pausedArg     = "--paused"
)

//go:embed tray/*.png
var trayFiles embed.FS

func trayIcon(paused bool) (png []byte, template bool) {
	// macOS tints a black template icon for the menu bar; the Windows
	// taskbar shows the app's own colors.
	name, template := "color", runtime.GOOS == "darwin"
	if template {
		name = "template"
	}
	if paused {
		name += "-paused"
	}
	png, _ = trayFiles.ReadFile("tray/" + name + ".png")
	return png, template
}

// system is the app's desktop: its window, tray, clipboard and updates.
type system struct {
	a        *app
	win      *mygo.Window
	tray     *mygo.Tray
	trayKey  string
	board    syncer.Board
	update   *mygo.Update
	logPath  string
	quitting bool
	showSoon bool
}

// post runs fn on the main thread and redraws the window.
func (s *system) post(fn func()) {
	mygo.RunOnMain(func() {
		fn()
		if s.win != nil {
			s.win.Invalidate()
		}
	})
}

func (s *system) startSync(cfg config, report syncer.Report) (func(), error) {
	if s.board == nil {
		board, err := newPasteboard()
		if err != nil {
			return nil, err
		}
		s.board = board
	}
	relay, err := client.New(cfg.Relay, cfg.Secret, cfg.Device)
	if err != nil {
		return nil, err
	}
	log.Printf("syncing through %s", cfg.Relay)
	ctx, cancel := context.WithCancel(context.Background())
	go syncer.Run(ctx, relay, s.board, func(e syncer.Event, err error) {
		s.post(func() { report(e, err) })
	})
	// The syncer may finish a send in flight after this returns, which a new
	// sync can't mix up: the relay orders copies by when they arrive.
	return cancel, nil
}

func (s *system) invite(cfg config, report func(inviteEvent)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	send := func(e inviteEvent) {
		s.post(func() {
			if ctx.Err() == nil {
				report(e)
			}
		})
	}
	go func() {
		relay, err := client.New(cfg.Relay, cfg.Secret, cfg.Device)
		for err == nil && ctx.Err() == nil {
			var inv *client.Invitation
			if inv, err = relay.Invite(ctx); err != nil {
				break
			}
			send(inviteEvent{code: inv.Code()})
			opened := time.Now()
			switch err = inv.Finish(ctx, client.Pairing{Relay: cfg.Relay, Secret: cfg.Secret}); {
			case ctx.Err() != nil:
				inv.Close()
			case err == nil:
				log.Print("a device joined by code")
				send(inviteEvent{joined: true})
			case errors.Is(err, client.ErrWrongCode):
				log.Print("a device tried a wrong code")
				send(inviteEvent{wrong: true})
				return
			case errors.Is(err, client.ErrCodeGone) && time.Since(opened) > time.Minute:
				// The code expired: make the next one. A relay that drops codes
				// sooner gets no more, rather than a code a second.
				err = nil
			default:
				inv.Close()
			}
		}
		if err != nil && ctx.Err() == nil {
			send(inviteEvent{err: err})
		}
	}()
	return cancel
}

func (s *system) joinCode(relay, code string, done func(client.Pairing, error)) func() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	// Only the main thread reads and writes stopped.
	stopped := false
	go func() {
		p, err := client.Join(ctx, relay, code)
		cancel()
		s.post(func() {
			if !stopped {
				done(p, err)
			}
		})
	}()
	return func() {
		stopped = true
		cancel()
	}
}

func (s *system) copySecret(text string) {
	if err := mygo.Clipboard.Write(transfer.TextData(text), mygo.ClipboardOptions{Concealed: true}); err != nil {
		log.Printf("copying: %v", err)
	}
}

func (s *system) openAtLogin() bool            { return mygo.App.OpenAtLogin() }
func (s *system) setOpenAtLogin(on bool) error { return mygo.App.SetOpenAtLogin(on) }
func (s *system) language() string             { return mygo.App.ChosenLocale() }
func (s *system) languages() []string          { return mygo.App.Languages() }
func (s *system) setLanguage(tag string)       { mygo.App.SetLocale(tag) }
func (s *system) openLogs()                    { mygo.Shell.ShowItemInFolder(s.logPath) }
func (s *system) changed()                     { s.refreshTray() }

func (s *system) show() {
	// A second launch can ask before the window exists.
	if s.win == nil {
		s.showSoon = true
		return
	}
	if runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	s.win.Restore()
	s.win.Show()
	s.win.Focus()
	mygo.App.Focus()
}

func (s *system) showTray(on bool) {
	if !on {
		if s.tray != nil {
			s.tray.Destroy()
			s.tray = nil
		}
		return
	}
	if s.tray != nil {
		return
	}
	icon, template := trayIcon(false)
	tray, err := mygo.NewTray(mygo.TrayOptions{ID: "clipsync", Icon: icon, IconIsTemplate: template, ToolTip: "clipsync"})
	if err != nil {
		log.Printf("tray: %v", err)
		return
	}
	s.tray, s.trayKey = tray, ""
	if runtime.GOOS == "windows" {
		// A click on a notification area icon opens the app there; its menu is a right-click.
		tray.OnClick(s.show)
		tray.OnRightClick(func() {
			tray.SetMenu(s.trayMenu())
			tray.PopUpMenu()
			tray.SetMenu(nil)
		})
	}
	s.refreshTray()
}

func (s *system) refreshTray() {
	if s.tray == nil || s.a == nil {
		return
	}
	a := s.a
	key := fmt.Sprint(a.sync, a.cfg.paired(), a.updates.stage, mygo.App.Locale())
	if key == s.trayKey {
		return
	}
	s.trayKey = key
	icon, template := trayIcon(!a.cfg.paired() || a.sync == syncPaused || a.sync == syncBroken)
	if err := s.tray.SetIcon(icon, template); err != nil {
		log.Printf("tray: %v", err)
	}
	status := locales.TrayNotSetUp()
	if a.cfg.paired() {
		status = statusLabel(a.sync)
	}
	s.tray.SetToolTip("clipsync: " + status)
	if runtime.GOOS != "windows" {
		s.tray.SetMenu(s.trayMenu())
	}
}

func (s *system) trayMenu() *mygo.Menu {
	a := s.a
	status := locales.TrayNotSetUp()
	if a.cfg.paired() {
		status = statusLabel(a.sync)
	}
	click := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			fn()
			s.win.Invalidate()
		}
	}
	items := []*mygo.MenuItem{
		{Label: status, Disabled: true},
		mygo.Separator(),
		{Label: locales.TrayOpen(), Click: click(s.show)},
	}
	if a.cfg.paired() {
		items = append(items, &mygo.MenuItem{Label: locales.TrayAddDevice(), Click: click(func() {
			a.page, a.adding = pageDevices, true
			s.show()
		})})
		switch a.sync {
		case syncPaused:
			items = append(items, &mygo.MenuItem{Label: locales.TrayResume(), Click: click(a.resume)})
		case syncBroken:
		default:
			items = append(items, &mygo.MenuItem{Label: locales.TrayPause(), Click: click(a.pause)})
		}
	}
	if a.updates.stage == updateReady {
		items = append(items, mygo.Separator(), &mygo.MenuItem{Label: locales.UpdateRestart(), Click: click(s.restart)})
	}
	return mygo.NewMenu(append(items, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit}))
}

// appMenu is the macOS menu bar while the window shows: the Edit menu makes
// the shortcuts to paste a link work.
func appMenu() *mygo.Menu {
	sep := mygo.Separator
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout}, sep(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, sep(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleEditMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo}, {Role: mygo.RoleRedo}, sep(),
			{Role: mygo.RoleCut}, {Role: mygo.RoleCopy}, {Role: mygo.RolePaste}, {Role: mygo.RoleSelectAll},
		}},
		{Role: mygo.RoleWindowMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize}, {Role: mygo.RoleZoom}, sep(), {Role: mygo.RoleClose},
		}},
	})
}

func (s *system) checkUpdates(manual bool) {
	u := &s.a.updates
	if !u.enabled || u.stage == updateChecking || u.stage == updateInstalling || u.stage == updateReady {
		return
	}
	u.stage = updateChecking
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		up, err := mygo.Updater.Check(ctx)
		s.post(func() {
			switch {
			case err != nil:
				log.Printf("checking for updates: %v", err)
				u.stage = updateIdle
				if manual {
					u.stage = updateFailed
				}
			case up == nil:
				u.stage = updateCurrent
			default:
				s.update, u.version, u.stage = up, up.Version, updateAvailable
				if !s.a.cfg.ManualUpdates {
					s.installUpdate()
				}
			}
			s.changed()
		})
	}()
}

func (s *system) installUpdate() {
	up := s.update
	if up == nil {
		return
	}
	u := &s.a.updates
	u.stage = updateInstalling
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := up.Install(ctx, nil)
		s.post(func() {
			if err != nil {
				log.Printf("installing the update: %v", err)
				u.stage = updateFailed
				return
			}
			log.Printf("installed %s", up.Version)
			u.stage = updateReady
			s.changed()
			// Nobody is looking, so take the update now.
			if !s.win.IsVisible() {
				mygo.App.Relaunch(s.relaunchArgs(backgroundArg)...)
			}
		})
	}()
}

func (s *system) restart() { mygo.App.Relaunch(s.relaunchArgs()...) }

func (s *system) relaunchArgs(args ...string) []string {
	if s.a.cfg.paired() && s.a.sync == syncPaused {
		args = append(args, pausedArg)
	}
	return args
}

// watchUpdates checks for updates a little after launch, then daily.
func (s *system) watchUpdates() {
	time.Sleep(30 * time.Second)
	for {
		s.post(func() { s.checkUpdates(false) })
		time.Sleep(24 * time.Hour)
	}
}

func appVersion() string { return mygo.App.Version() }

func openLog() (string, error) {
	dir, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "clipsync.log")
	flags := os.O_CREATE | os.O_APPEND | os.O_WRONLY
	if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return "", err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return path, nil
}

func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	s := &system{}
	var err error
	if s.logPath, err = openLog(); err != nil {
		log.Printf("logging: %v", err)
	}
	path, err := configPath()
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		// Keep the old file, which may hold the only copy of the secret.
		log.Printf("starting afresh: %v", err)
		if err := os.Rename(path, path+".bad"); err != nil {
			log.Fatal(err)
		}
		cfg = config{Relay: publicRelay}
	}

	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { s.quitting = true })
	mygo.App.OnQuitCanceled(func() { s.quitting = false })
	mygo.App.OnSecondInstance(func([]string, string) { s.show() })
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			s.show()
		}
	})
	mygo.App.OnLocaleChanged(func(string) {
		s.trayKey = ""
		s.refreshTray()
	})
	mygo.App.WhenReady(func() {
		replaced, err := removeCLIAgent()
		if err != nil {
			log.Printf("stopping the clipsync CLI: %v", err)
		}
		s.a = newApp(s, path, cfg, slices.Contains(os.Args[1:], pausedArg))
		s.a.updates.enabled = mygo.Updater.Enabled()
		if replaced {
			log.Print("replaced the clipsync CLI's background service")
			if err := mygo.App.SetOpenAtLogin(true); err != nil {
				log.Printf("opening at login: %v", err)
			}
		}
		hidden := s.a.cfg.paired() && !s.showSoon && (mygo.App.WasOpenedAtLogin() || slices.Contains(os.Args[1:], backgroundArg))
		opts := mygo.WindowOptions{
			Title:     "clipsync",
			Width:     800,
			Height:    580,
			MinWidth:  660,
			MinHeight: 500,
			StateKey:  "main",
			Hidden:    hidden,
			Content:   ui.View(s.a.view),
		}
		if runtime.GOOS == "darwin" {
			opts.TitleBarStyle, opts.Vibrancy = mygo.TitleBarHidden, mygo.VibrancySidebar
			if !hidden {
				mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
			}
			mygo.App.SetMenu(appMenu())
		} else {
			mygo.App.SetMenu(nil)
		}
		s.win = mygo.NewWindow(opts)
		s.win.OnClose(func(e *mygo.CloseEvent) {
			// Closing the window keeps clipsync syncing; Quit stops it.
			if !s.quitting {
				e.PreventDefault()
				s.win.Hide()
			}
		})
		s.win.OnHide(func() {
			s.a.hidden()
			if runtime.GOOS == "darwin" {
				// The Dock shows clipsync only while its window does.
				mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
			}
		})
		s.showTray(!s.a.cfg.HideTray)
		go s.watchUpdates()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

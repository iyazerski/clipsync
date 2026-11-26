package main

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/iyazerski/clipsync/internal/client"
	"github.com/iyazerski/clipsync/internal/syncer"
)

type fakeDesktop struct {
	syncing bool
	report  syncer.Report
	copied  string
	login   bool

	inviting bool
	invited  func(inviteEvent)
	joining  string
	join     func(client.Pairing, error)
}

func (f *fakeDesktop) startSync(_ config, report syncer.Report) (func(), error) {
	f.syncing, f.report = true, report
	return func() { f.syncing = false }, nil
}
func (f *fakeDesktop) copySecret(text string)       { f.copied = text }
func (f *fakeDesktop) openAtLogin() bool            { return f.login }
func (f *fakeDesktop) setOpenAtLogin(on bool) error { f.login = on; return nil }
func (f *fakeDesktop) showTray(bool)                {}
func (f *fakeDesktop) language() string             { return "" }
func (f *fakeDesktop) languages() []string          { return []string{"en"} }
func (f *fakeDesktop) setLanguage(string)           {}
func (f *fakeDesktop) checkUpdates(bool)            {}
func (f *fakeDesktop) installUpdate()               {}
func (f *fakeDesktop) restart()                     {}
func (f *fakeDesktop) openLogs()                    {}
func (f *fakeDesktop) changed()                     {}

func (f *fakeDesktop) invite(_ config, report func(inviteEvent)) func() {
	f.inviting, f.invited = true, report
	return func() { f.inviting = false }
}

func (f *fakeDesktop) joinCode(_, code string, done func(client.Pairing, error)) func() {
	f.joining, f.join = code, done
	return func() { f.joining = "" }
}

func start(t *testing.T, cfg config) (*app, *fakeDesktop, *ui.Tester) {
	t.Helper()
	sys := &fakeDesktop{}
	a := newApp(sys, filepath.Join(t.TempDir(), "config.json"), cfg, false)
	return a, sys, ui.NewTester(a.view, 800, 1000)
}

func click(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if err := tt.Click(name); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
}

func TestFirstDevice(t *testing.T) {
	a, sys, tt := start(t, config{Relay: publicRelay})
	click(t, tt, "Set up my first device")

	saved, err := loadConfig(a.path)
	if err != nil || !saved.paired() || saved.Device == "" {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if !sys.syncing || !sys.login {
		t.Errorf("syncing %v, opens at login %v", sys.syncing, sys.login)
	}
	if !sys.inviting {
		t.Fatal("no code to add a device with")
	}
	sys.invited(inviteEvent{code: "42183059"})
	click(t, tt, "Computer")
	if !tt.HasText("42 183 059") {
		t.Errorf("code not shown; texts %q", tt.Texts())
	}
	// A wrong guess ends the code, and only the user makes the next.
	sys.invited(inviteEvent{wrong: true})
	tt.Frame()
	if sys.inviting || tt.HasText("42 183 059") {
		t.Errorf("still inviting after a wrong code; texts %q", tt.Texts())
	}
	click(t, tt, "New code")
	if !sys.inviting {
		t.Error("New code made no code")
	}
	sys.invited(inviteEvent{code: "42183059"})
	tt.Frame()
	click(t, tt, "Copy link")
	if sys.copied != saved.pairLink() || tt.HasText(saved.pairLink()) {
		t.Errorf("copied %q, link shown %v", sys.copied, tt.HasText(saved.pairLink()))
	}

	sys.report(syncer.Connected, nil)
	click(t, tt, "Done")
	if sys.inviting {
		t.Error("still inviting after Done")
	}
	if !tt.HasText("Syncing") {
		t.Errorf("texts %q", tt.Texts())
	}
	click(t, tt, "Pause")
	if sys.syncing || !tt.HasText("Paused") {
		t.Errorf("syncing %v after pausing, texts %q", sys.syncing, tt.Texts())
	}
}

func TestJoin(t *testing.T) {
	a, sys, tt := start(t, config{Relay: publicRelay})
	click(t, tt, "Join my other devices")
	click(t, tt, "Code or pairing link")
	tt.Type("not a code")
	click(t, tt, "Join")
	if a.cfg.paired() || !tt.HasText("Enter the code from your other device, such as 42 183 059, or paste its pairing link.") {
		t.Fatalf("paired %v, texts %q", a.cfg.paired(), tt.Texts())
	}

	tt.SetClipboard("42-183 059")
	click(t, tt, "Paste")
	click(t, tt, "Join")
	if sys.joining != "42183059" {
		t.Fatalf("joining with %q", sys.joining)
	}
	sys.join(client.Pairing{}, client.ErrWrongCode)
	tt.Frame()
	if a.cfg.paired() || !tt.HasText("That code didn't work. Check it, or get a new code on your other device.") {
		t.Fatalf("paired %v, texts %q", a.cfg.paired(), tt.Texts())
	}

	click(t, tt, "Join")
	other := config{Relay: "https://clip.example.com", Secret: client.RandomID(32)}
	sys.join(client.Pairing{Relay: other.Relay, Secret: other.Secret}, nil)
	tt.Frame()
	saved, err := loadConfig(a.path)
	if err != nil || saved.Relay != other.Relay || saved.Secret != other.Secret || !sys.syncing {
		t.Fatalf("saved %+v, %v, syncing %v", saved, err, sys.syncing)
	}

	click(t, tt, "Settings")
	click(t, tt, "Unpair…")
	click(t, tt, "Unpair")
	if saved, _ := loadConfig(a.path); saved.paired() || sys.syncing || !tt.HasText("Welcome to clipsync") {
		t.Errorf("saved %+v, syncing %v, texts %q", saved, sys.syncing, tt.Texts())
	}
}

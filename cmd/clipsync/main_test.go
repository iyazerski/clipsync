//go:build darwin || windows

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

type fakeAgent struct{}

func (fakeAgent) install() (string, error) { return "test.log", nil }
func (fakeAgent) uninstall() (bool, error) { return false, nil }
func (fakeAgent) restart() (bool, error)   { return false, nil }

func TestPairingCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", t.TempDir())
	} else {
		t.Setenv("HOME", t.TempDir())
	}
	var out strings.Builder
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		if err := cli(t.Context(), args, &out, fakeAgent{}); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if err := cli(t.Context(), []string{"setup", "--relay", "http://example.test"}, &out, fakeAgent{}); err == nil {
		t.Fatal("setup accepted an insecure relay")
	}
	// Setup can run before the relay accepts the pairing token.
	relay := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(relay.Close)
	fixture, err := os.ReadFile("../../internal/syncer/testdata/pairing.txt")
	if err != nil {
		t.Fatal(err)
	}
	fixtureSecret, fixtureToken, ok := strings.Cut(strings.TrimSpace(string(fixture)), "\n")
	if !ok {
		t.Fatal("pairing fixture needs a secret and token")
	}
	address := relay.URL
	run("setup", "--relay", address+"/", "--secret", fixtureSecret)
	run("setup", "--relay", address)
	if got := strings.TrimSpace(run("token")); got != fixtureToken {
		t.Fatalf("token is incompatible with the existing relay: %q", got)
	}
	if got := run("pair"); !strings.Contains(got, "clipsync setup --relay "+address+" --secret "+fixtureSecret) {
		t.Fatal("pair did not print the setup command")
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("config is not private: %v", info.Mode())
	}
}

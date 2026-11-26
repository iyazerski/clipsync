//go:build darwin || windows

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// Without --relay, setup keeps the relay that the computer already uses.
	run("setup")
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

func TestUnpackArchive(t *testing.T) {
	t.Run("tar.gz", func(t *testing.T) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		payload := []byte("mac-binary")
		if err := tw.WriteHeader(&tar.Header{Name: "./clipsync", Mode: 0755, Size: int64(len(payload))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		if err := unpackArchive(buf.Bytes(), dir, []string{"clipsync"}, false); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "clipsync"))
		if err != nil || string(got) != string(payload) {
			t.Fatalf("got %q (%v), want %q", got, err, payload)
		}
		if err := unpackArchive(buf.Bytes(), t.TempDir(), []string{"clipsync", "missing"}, false); err == nil {
			t.Fatal("expected error for missing binary")
		}
	})

	t.Run("zip", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, content := range map[string]string{
			"clipsync.exe":  "cli-bin",
			"clipsyncw.exe": "gui-bin",
		} {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		if err := unpackArchive(buf.Bytes(), dir, []string{"clipsync.exe", "clipsyncw.exe"}, true); err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]string{
			"clipsync.exe":  "cli-bin",
			"clipsyncw.exe": "gui-bin",
		} {
			got, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || string(got) != want {
				t.Fatalf("%s = %q (%v), want %q", name, got, err, want)
			}
		}
	})
}

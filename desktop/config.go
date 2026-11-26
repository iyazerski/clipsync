package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/iyazerski/clipsync/internal/client"
)

const publicRelay = "https://clip.iyazerski.dev"

// config is what the CLI kept in the same file, so its users keep their pairing.
type config struct {
	Relay    string `json:"relay"`
	Secret   string `json:"secret"`
	Device   string `json:"device"`
	HideTray bool   `json:"hideTray,omitempty"`
	// ManualUpdates leaves installing updates to the user.
	ManualUpdates bool `json:"manualUpdates,omitempty"`
}

func (c config) paired() bool { return c.Secret != "" }

// pairLink is what a phone scans and another computer pastes. The secret is in
// the fragment, which browsers don't send to the relay.
func (c config) pairLink() string { return c.Relay + "/pair#" + c.Secret }

var errNotLink = errors.New("not a pairing link")

func parsePairLink(link string) (relay, secret string, err error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Fragment == "" {
		return "", "", errNotLink
	}
	base, ok := strings.CutSuffix(u.Scheme+"://"+u.Host+u.Path, "/pair")
	if !ok {
		return "", "", errNotLink
	}
	if relay, err = client.NormalizeRelay(base); err != nil {
		return "", "", err
	}
	if _, err := client.Token(u.Fragment); err != nil {
		return "", "", err
	}
	return relay, u.Fragment, nil
}

func configPath() (string, error) {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "clipsync", "config.json"), nil
		}
		return "", errors.New("LOCALAPPDATA is not set")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clipsync", "config.json"), nil
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config{Relay: publicRelay}, nil
	}
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if cfg.Relay, err = client.NormalizeRelay(cmp.Or(cfg.Relay, publicRelay)); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func saveConfig(path string, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := errors.Join(enc.Encode(cfg), f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

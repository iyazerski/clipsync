//go:build darwin || windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/skip2/go-qrcode"

	"github.com/iyazerski/clipsync/internal/client"
)

var version = "0.1.0"

const help = `Usage: clipsync <command>

  setup --relay <https://url> [--secret <secret>]   Pair and start syncing at login
  pair                                            Show pairing QR and command
  token                                           Print the relay token
  run                                             Sync in the foreground
  uninstall                                       Stop syncing at login (keep settings)
  update                                          Install the latest release
  --version                                       Print the version
`

type agent interface {
	install() (string, error)
	uninstall() (bool, error)
	restart() (bool, error)
}

type systemAgent struct{}

type config struct {
	Relay  string `json:"relay"`
	Secret string `json:"secret"`
	Device string `json:"device"`
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
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("reading %s: %w", path, err)
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

func cli(ctx context.Context, args []string, out io.Writer, service agent) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, help)
		return nil
	}
	switch args[0] {
	case "--version", "-V":
		fmt.Fprintln(out, "clipsync", version)
		return nil
	case "setup", "pair", "token", "run", "update", "uninstall", "service":
	default:
		return fmt.Errorf("unknown command %q; see --help", args[0])
	}
	if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
		fmt.Fprint(out, help)
		return nil
	}
	if args[0] != "setup" && len(args) != 1 {
		return fmt.Errorf("%s does not take arguments", args[0])
	}
	switch args[0] {
	case "service":
		return supervise(ctx)
	case "update":
		return update(ctx, out, service)
	case "uninstall":
		stopped, err := service.uninstall()
		if err != nil {
			return err
		}
		if stopped {
			fmt.Fprintln(out, "stopped; clipsync no longer runs in the background")
		} else {
			fmt.Fprintln(out, "clipsync wasn't running in the background")
		}
		return nil
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	if args[0] == "setup" {
		return setup(ctx, path, args[1:], out, service)
	}
	cfg, err := loadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("not set up yet: run clipsync setup --relay <url> first")
	}
	if err != nil {
		return err
	}
	cfg.Relay, err = client.NormalizeRelay(cfg.Relay)
	if err != nil {
		return err
	}
	switch args[0] {
	case "pair":
		return pair(cfg, out)
	case "token":
		token, err := client.Token(cfg.Secret)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, token)
	case "run":
		board, err := newPasteboard()
		if err != nil {
			return err
		}
		return runSync(ctx, cfg, board)
	}
	return nil
}

func setup(ctx context.Context, path string, args []string, out io.Writer, service agent) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("relay", "", "HTTPS relay URL")
	secret := flags.String("secret", "", "shared secret")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return errors.New("setup takes only --relay and --secret flags")
	}
	addressValue, err := client.NormalizeRelay(*address)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cfg.Relay = addressValue
	if cfg.Device == "" {
		prefix := "mac"
		if runtime.GOOS == "windows" {
			prefix = "win"
		}
		cfg.Device = prefix + "-" + client.RandomID(9)
	}
	explicitSecret := false
	flags.Visit(func(f *flag.Flag) { explicitSecret = explicitSecret || f.Name == "secret" })
	if explicitSecret {
		cfg.Secret = strings.TrimSpace(*secret)
	} else if cfg.Secret == "" {
		cfg.Secret = client.RandomID(32)
	}
	r, err := client.New(cfg.Relay, cfg.Secret, cfg.Device)
	if err != nil {
		return err
	}
	if err := saveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintln(out, "saved to", path)
	switch err := r.Check(ctx); {
	case err == nil:
		fmt.Fprintln(out, "the relay accepts this secret")
	case errors.Is(err, client.ErrRefused):
		fmt.Fprintln(out, "the relay refused the token; set its CLIPSYNC_TOKEN to the output of clipsync token")
	default:
		fmt.Fprintf(out, "couldn't reach the relay (%v)\n", err)
	}
	logPath, err := service.install()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "clipsync now runs in the background and starts at login; log: %s\n", logPath)
	fmt.Fprintln(out, "run clipsync pair to add your phone and other computers")
	return nil
}

func pair(cfg config, out io.Writer) error {
	if _, err := client.Token(cfg.Secret); err != nil {
		return err
	}
	// The browser doesn't send the secret in the fragment to the relay.
	qr, err := qrcode.New(cfg.Relay+"/pair#"+cfg.Secret, qrcode.Low)
	if err != nil {
		return fmt.Errorf("making the QR code: %w", err)
	}
	fmt.Fprint(out, "Phone: scan the QR code and tap Open clipsync.\n\n", qr.ToSmallString(true), "\n")
	fmt.Fprintf(out, "Another computer:\n  clipsync setup --relay %s --secret %s\n", cfg.Relay, cfg.Secret)
	fmt.Fprintln(out, "Keep the secret private: anyone with it can read and change what you copy.")
	return nil
}

func main() {
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli(ctx, os.Args[1:], os.Stdout, systemAgent{}); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

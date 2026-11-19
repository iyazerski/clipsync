package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const launchLabel = "dev.iyazerski.clipsync"

func supervise(context.Context) error {
	return errors.New("unknown command \"service\"; see --help")
}

func launchPaths() (plist, log string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist"),
		filepath.Join(home, "Library", "Logs", "clipsync.log"), nil
}

func launchctl(args ...string) error {
	out, err := exec.Command("/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func launchDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func (systemAgent) install() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	plist, log, err := launchPaths()
	if err != nil {
		return "", err
	}
	for _, path := range []string{plist, log} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
	}
	escape := func(text string) string {
		var out strings.Builder
		xml.EscapeText(&out, []byte(text))
		return out.String()
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>30</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, launchLabel, escape(exe), escape(log), escape(log))
	_ = launchctl("bootout", launchDomain()+"/"+launchLabel)
	if err := os.WriteFile(plist, []byte(body), 0644); err != nil {
		return "", err
	}
	return log, launchctl("bootstrap", launchDomain(), plist)
}

func (systemAgent) restart() (bool, error) {
	plist, _, err := launchPaths()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(plist); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, launchctl("kickstart", "-k", launchDomain()+"/"+launchLabel)
}

func (systemAgent) uninstall() (bool, error) {
	plist, _, err := launchPaths()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(plist); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	_ = launchctl("bootout", launchDomain()+"/"+launchLabel)
	return true, os.Remove(plist)
}

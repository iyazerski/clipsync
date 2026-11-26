package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// removeCLIAgent stops the launch agent of the clipsync CLI, which this app replaces.
func removeCLIAgent() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	const label = "dev.iyazerski.clipsync"
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if _, err := os.Stat(plist); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	// bootout fails when the agent isn't loaded, which is fine.
	_ = exec.Command("/bin/launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
	return true, os.Remove(plist)
}

func computerName() string {
	if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	name, _ := os.Hostname()
	return strings.TrimSuffix(name, ".local")
}

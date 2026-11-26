package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// removeCLIAgent stops the background service of the clipsync CLI, which this
// app replaces: its Run entry, its process, and its windowless executable,
// which sits in the folder the app now installs to.
func removeCLIAgent() (bool, error) {
	const runKey, runValue = `Software\Microsoft\Windows\CurrentVersion\Run`, "clipsync"
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	defer key.Close()
	if err := key.DeleteValue(runValue); errors.Is(err, registry.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	kill := exec.Command("taskkill", "/F", "/T", "/IM", "clipsyncw.exe")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = kill.Run()
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(filepath.Join(filepath.Dir(exe), "clipsyncw.exe"))
	}
	return true, nil
}

func computerName() string {
	name, _ := os.Hostname()
	return name
}

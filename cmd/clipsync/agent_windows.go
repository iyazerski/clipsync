package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

const (
	runKey     = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue   = "clipsync"
	windowless = "clipsyncw.exe"
)

func (systemAgent) install() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	service := filepath.Join(filepath.Dir(exe), windowless)
	if _, err := os.Stat(service); err != nil {
		return "", err
	}
	path, err := configPath()
	if err != nil {
		return "", err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	if err := key.SetStringValue(runValue, `"`+service+`" service`); err != nil {
		return "", err
	}
	stopAgent()
	return filepath.Join(filepath.Dir(path), "clipsync.log"), startAgent(service)
}

func startAgent(service string) error {
	cmd := exec.Command(service, "service")
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func stopAgent() {
	cmd := exec.Command("taskkill", "/F", "/T", "/IM", windowless)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}

func (systemAgent) restart() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer key.Close()
	if _, _, err := key.GetStringValue(runValue); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	stopAgent()
	return true, startAgent(filepath.Join(filepath.Dir(exe), windowless))
}

func (systemAgent) uninstall() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer key.Close()
	if err := key.DeleteValue(runValue); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	stopAgent()
	return true, nil
}

func supervise(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		log, err := os.OpenFile(filepath.Join(filepath.Dir(path), "clipsync.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, exe, "run")
		cmd.Stdout, cmd.Stderr = log, log
		err = cmd.Run()
		log.Close()
		if err == nil || ctx.Err() != nil {
			return nil
		}
		if _, exited := errors.AsType[*exec.ExitError](err); !exited {
			return err
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	return nil
}

//go:build !darwin && !windows

// clipsync runs on macOS and Windows. This lets the views build and test on
// Linux too, as in CI.
package main

import (
	"errors"
	"os"

	"github.com/iyazerski/clipsync/internal/syncer"
)

func newPasteboard() (syncer.Board, error) {
	return nil, errors.New("clipsync syncs the clipboard on macOS and Windows only")
}

func removeCLIAgent() (bool, error) { return false, nil }

func computerName() string {
	name, _ := os.Hostname()
	return name
}

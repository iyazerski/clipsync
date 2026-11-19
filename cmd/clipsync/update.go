//go:build darwin || windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const releaseAPI = "https://api.github.com/repos/iyazerski/clipsync/releases/latest"

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) asset(name string) (string, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("release %s has no %s", r.Tag, name)
}

func update(ctx context.Context, out io.Writer, service agent) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && slices.Contains(strings.Split(resolved, string(filepath.Separator)), "Cellar") {
		return errors.New("clipsync was installed with Homebrew: run brew upgrade clipsync instead")
	}
	httpClient := &http.Client{Timeout: 60 * time.Second}
	get := func(address string, limit int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "clipsync/"+version)
		if address == releaseAPI {
			req.Header.Set("Accept", "application/vnd.github+json")
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("checking for updates: HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if int64(len(body)) > limit {
			return nil, errors.New("the update download is too large")
		}
		return body, err
	}
	body, err := get(releaseAPI, 1<<20)
	if err != nil {
		return err
	}
	var published release
	if err := json.Unmarshal(body, &published); err != nil {
		return err
	}
	latest := strings.TrimPrefix(published.Tag, "v")
	if !newer(latest, version) {
		fmt.Fprintf(out, "clipsync %s is up to date\n", version)
		return nil
	}
	fmt.Fprintf(out, "downloading clipsync %s…\n", latest)
	asset, names := "clipsync-aarch64-apple-darwin.tar.gz", []string{"clipsync"}
	tar := "/usr/bin/tar"
	if runtime.GOOS == "windows" {
		asset = "clipsync-x86_64-pc-windows-msvc.zip"
		names = []string{"clipsync.exe", "clipsyncw.exe"}
		tar = "tar"
	}
	archiveURL, err := published.asset(asset)
	if err != nil {
		return err
	}
	checksumURL, err := published.asset(asset + ".sha256")
	if err != nil {
		return err
	}
	archive, err := get(archiveURL, 64<<20)
	if err != nil {
		return err
	}
	checksum, err := get(checksumURL, 4096)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(checksum))
	if len(fields) == 0 || fields[0] != fmt.Sprintf("%x", sha256.Sum256(archive)) {
		return errors.New("the download doesn't match its checksum; try again")
	}

	dir := filepath.Dir(exe)
	staging, err := os.MkdirTemp(dir, ".clipsync-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	packed := filepath.Join(staging, asset)
	if err := os.WriteFile(packed, archive, 0600); err != nil {
		return err
	}
	args := append([]string{"-xf", packed, "-C", staging}, names...)
	if data, err := exec.CommandContext(ctx, tar, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("unpacking update: %w: %s", err, data)
	}
	for i, name := range names {
		target := filepath.Join(dir, name)
		if i == 0 {
			target = exe
		}
		if err := swapExecutable(filepath.Join(staging, name), target); err != nil {
			return fmt.Errorf("replacing %s: %w", target, err)
		}
	}
	fmt.Fprintf(out, "updated clipsync %s → %s\n", version, latest)
	restarted, err := service.restart()
	if restarted && err == nil {
		fmt.Fprintln(out, "restarted the background service")
	}
	return err
}

func swapExecutable(from, target string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(from, target)
	}
	// Windows won't replace a running executable, but it can rename it.
	old := target + ".old"
	_ = os.Remove(old)
	if err := os.Rename(target, old); err != nil {
		return err
	}
	if err := os.Rename(from, target); err != nil {
		return errors.Join(err, os.Rename(old, target))
	}
	return nil
}

func newer(a, b string) bool {
	parts := func(version string) []uint64 {
		var result []uint64
		for _, part := range strings.Split(version, ".") {
			n, _ := strconv.ParseUint(part, 10, 64)
			result = append(result, n)
		}
		return result
	}
	return slices.Compare(parts(a), parts(b)) > 0
}

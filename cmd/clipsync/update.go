//go:build darwin || windows

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	releaseAPI    = "https://api.github.com/repos/iyazerski/clipsync/releases/latest"
	maxUpdateSize = 64 << 20
)

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
	if runtime.GOOS == "windows" {
		asset = "clipsync-x86_64-pc-windows-msvc.zip"
		names = []string{"clipsync.exe", "clipsyncw.exe"}
	}
	archiveURL, err := published.asset(asset)
	if err != nil {
		return err
	}
	checksumURL, err := published.asset(asset + ".sha256")
	if err != nil {
		return err
	}
	archive, err := get(archiveURL, maxUpdateSize)
	if err != nil {
		return err
	}
	checksum, err := get(checksumURL, 4096)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(checksum))
	if len(fields) == 0 || !strings.EqualFold(fields[0], fmt.Sprintf("%x", sha256.Sum256(archive))) {
		return errors.New("the download doesn't match its checksum; try again")
	}

	dir := filepath.Dir(exe)
	staging, err := os.MkdirTemp(dir, ".clipsync-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := unpackArchive(archive, staging, names, strings.HasSuffix(asset, ".zip")); err != nil {
		return fmt.Errorf("unpacking update: %w", err)
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

func unpackArchive(archive []byte, staging string, names []string, isZip bool) error {
	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[name] = true
	}
	if isZip {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return err
		}
		for _, f := range zr.File {
			name := path.Clean(f.Name)
			if f.FileInfo().IsDir() || !want[name] {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = writeExecutable(filepath.Join(staging, name), rc)
			rc.Close()
			if err != nil {
				return err
			}
			delete(want, name)
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			name := path.Clean(hdr.Name)
			if hdr.Typeflag != tar.TypeReg || !want[name] {
				continue
			}
			if err := writeExecutable(filepath.Join(staging, name), tr); err != nil {
				return err
			}
			delete(want, name)
		}
	}
	for _, name := range names {
		if want[name] {
			return fmt.Errorf("archive is missing %s", name)
		}
	}
	return nil
}

func writeExecutable(dst string, src io.Reader) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(src, maxUpdateSize+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if n > maxUpdateSize {
		return errors.New("the unpacked update is too large")
	}
	return nil
}

func newer(a, b string) bool {
	parts := func(version string) []uint64 {
		var result []uint64
		for part := range strings.SplitSeq(version, ".") {
			n, _ := strconv.ParseUint(part, 10, 64)
			result = append(result, n)
		}
		return result
	}
	return slices.Compare(parts(a), parts(b)) > 0
}

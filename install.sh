#!/usr/bin/env sh
# Install the latest clipsync release into ~/.local/bin.
# Set CLIPSYNC_VERSION=vX.Y.Z to pin a release.
set -eu

repo="iyazerski/clipsync"
asset="clipsync-aarch64-apple-darwin.tar.gz"

if [ "$(uname -s)-$(uname -m)" != "Darwin-arm64" ]; then
  echo "clipsync supports macOS on Apple silicon only." >&2
  exit 1
fi

if [ -n "${CLIPSYNC_VERSION:-}" ]; then
  download_url="https://github.com/$repo/releases/download/$CLIPSYNC_VERSION/$asset"
else
  download_url="https://github.com/$repo/releases/latest/download/$asset"
fi
install_tmp="$(mktemp -d)"
trap 'rm -rf "$install_tmp"' EXIT HUP INT TERM

echo "Downloading $asset..."
curl -fsSL "$download_url" -o "$install_tmp/$asset"
curl -fsSL "$download_url.sha256" -o "$install_tmp/$asset.sha256"
(cd "$install_tmp" && shasum -a 256 -c "$asset.sha256")

tar -xzf "$install_tmp/$asset" -C "$install_tmp"

mkdir -p "$HOME/.local/bin"
install -m 755 "$install_tmp/clipsync" "$HOME/.local/bin/clipsync"

PATH="$HOME/.local/bin:$PATH"
export PATH

if ! command -v clipsync >/dev/null 2>&1; then
  echo "clipsync installed, but it is not on PATH. Add $HOME/.local/bin to PATH." >&2
  exit 1
fi

echo "$(clipsync --version) is installed to $HOME/.local/bin/clipsync"
# A running service keeps the old binary: restart it on the new one.
if [ -f "$HOME/Library/LaunchAgents/dev.iyazerski.clipsync.plist" ]; then
  launchctl kickstart -k "gui/$(id -u)/dev.iyazerski.clipsync" && echo "restarted the background service"
fi

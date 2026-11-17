#!/bin/sh
# Print the Homebrew formula for clipsync VERSION, taking the checksum from the
# Mac archive's .sha256 file in DIST. The release workflow writes it to
# Formula/clipsync.rb in iyazerski/homebrew-tap.
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 VERSION DIST" >&2
  exit 2
fi
version="$1"
asset="clipsync-aarch64-apple-darwin.tar.gz"
sha256="$(cut -d' ' -f1 "$2/$asset.sha256")"

cat <<EOF
class Clipsync < Formula
  desc "Sync the clipboard between Mac, Windows and Android through your own relay"
  homepage "https://github.com/iyazerski/clipsync"
  url "https://github.com/iyazerski/clipsync/releases/download/v$version/$asset"
  sha256 "$sha256"
  license "MIT"

  depends_on arch: :arm64
  depends_on :macos

  def install
    bin.install "clipsync"
  end

  # The background service keeps running the old version until it restarts. Without
  # one (before \`clipsync setup\`), kickstart fails, and that's fine.
  post_install_steps do
    run "/bin/sh", args: ["-c", 'launchctl kickstart -k "gui/\$(id -u)/dev.iyazerski.clipsync"'],
                   must_succeed: false, print_stderr: false
  end

  def caveats
    <<~EOS
      To start syncing, run:
        clipsync setup --relay https://clip.example.com
      with your relay's address. To host a relay, see
        https://github.com/iyazerski/clipsync#get-started
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/clipsync --version")
  end
end
EOF

#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

printf '# Third-party licenses\n\n'
license() {
  printf '## %s\n\n```text\n' "$1"
  cat "$2"
  printf '\n```\n\n'
}

goroot=$(go env GOROOT)
go_license="$goroot/LICENSE"
if [ ! -f "$go_license" ]; then
  # Homebrew installs the license beside libexec rather than in GOROOT.
  go_license="$(dirname "$goroot")/LICENSE"
fi
license "Go standard library and runtime" "$go_license"
for module in github.com/ebitengine/purego github.com/skip2/go-qrcode golang.org/x/sys; do
  go mod download "$module"
  dir=$(go list -m -f '{{.Dir}}' "$module")
  license "$module" "$dir/LICENSE"
  if [ -f "$dir/PATENTS" ]; then
    license "$module patent grant" "$dir/PATENTS"
  fi
done
cat android/licenses.md

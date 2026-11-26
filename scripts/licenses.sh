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
# The modules the desktop app and the relay link, on any system.
modules=$(for os in darwin windows; do
  GOOS=$os go list -mod=readonly -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}{{end}}{{end}}' ./desktop ./relay
done | sort -u)
for module in $modules; do
  go mod download "$module"
  dir=$(go list -m -f '{{.Dir}}' "$module")
  license "$module" "$dir/LICENSE"
  if [ -f "$dir/PATENTS" ]; then
    license "$module patent grant" "$dir/PATENTS"
  fi
done
cat android/licenses.md
printf '\n## Material Symbols\n\nThe desktop app'"'"'s icons are Google'"'"'s Material Symbols, under the Apache License 2.0,\nwhose full text is above.\n'

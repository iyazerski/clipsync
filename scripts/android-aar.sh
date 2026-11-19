#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: $0 /absolute/path/to/clipsync.aar" >&2
  exit 2
fi
out=$1
sdk=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}
if [ -z "$sdk" ]; then
  echo "set ANDROID_HOME to the Android SDK directory" >&2
  exit 1
fi
export ANDROID_HOME="$sdk"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$sdk/ndk/27.2.12479018}"
cd "$(dirname "$0")/.."
tools="$(mktemp -d)"
trap 'rm -rf "$tools"' EXIT
go build -mod=readonly -o "$tools/gobind" golang.org/x/mobile/cmd/gobind
mkdir -p "$(dirname "$out")"
PATH="$tools:$PATH" go tool gomobile bind -target=android -androidapi=34 \
  -javapkg=dev.iyazerski -ldflags='-s -w -extldflags=-Wl,-z,max-page-size=16384' \
  -o "$out" ./androidbridge

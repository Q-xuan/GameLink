#!/bin/sh
# Download official Wintun 0.14.1 and stage the amd64 DLL for go:embed.
# Zip and DLL digests match internal/wintunbin/pin.go.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
zip_sha=07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
dll_sha=e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce
cache="$root/.cache"
mkdir -p "$cache"
zip="$cache/wintun-0.14.1.zip"
curl -fsSL -o "$zip" "https://www.wintun.net/builds/wintun-0.14.1.zip"
got=$(sha256sum "$zip" | awk '{print $1}')
if [ "$got" != "$zip_sha" ]; then
  echo "wintun zip sha256 $got" >&2
  exit 1
fi
rm -rf "$cache/wintun"
unzip -q -o "$zip" -d "$cache/wintun"
dll="$cache/wintun/wintun/bin/amd64/wintun.dll"
gotdll=$(sha256sum "$dll" | awk '{print $1}')
if [ "$gotdll" != "$dll_sha" ]; then
  echo "wintun.dll sha256 $gotdll" >&2
  exit 1
fi
cp "$dll" "$root/internal/wintunbin/wintun.dll"
echo "staged internal/wintunbin/wintun.dll"

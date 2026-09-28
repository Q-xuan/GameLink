// Package wintunbin pins the official Wintun 0.14.1 amd64 DLL.
// CI downloads the zip, checks ZipSHA256, and stages wintun.dll before compile.
package wintunbin

const (
	// Version is the only Wintun release this client embeds.
	Version = "0.14.1"
	// Arch is the only DLL architecture this client embeds.
	Arch = "amd64"
	// ZipSHA256 is the official wintun-0.14.1.zip digest published on wintun.net.
	ZipSHA256 = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
	// DLLSHA256 is the digest of wintun/bin/amd64/wintun.dll inside that zip.
	DLLSHA256 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
	// FileName is the DLL written beside the executable.
	FileName = "wintun.dll"
)

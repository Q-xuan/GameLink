//go:build windows && amd64

package wintunbin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "embed"
)

//go:embed wintun.dll
var dll []byte

// Install checks the embedded official DLL, then writes it beside the
// executable from os.Executable. It refuses to start when the bytes do
// not match DLLSHA256. The working directory is not used.
func Install() error {
	sum := sha256.Sum256(dll)
	if hex.EncodeToString(sum[:]) != DLLSHA256 {
		return errors.New("内置 wintun.dll 与官方 Wintun 0.14.1 amd64 校验值不一致，拒绝启动")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位程序自身: %w", err)
	}
	dest := filepath.Join(filepath.Dir(exe), FileName)
	if existing, err := os.ReadFile(dest); err == nil {
		got := sha256.Sum256(existing)
		if hex.EncodeToString(got[:]) == DLLSHA256 {
			return nil
		}
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, dll, 0o644); err != nil {
		return fmt.Errorf("无法写出 wintun.dll: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(dest)
		if err2 := os.Rename(tmp, dest); err2 != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("无法替换 wintun.dll: %w", err2)
		}
	}
	written, err := os.ReadFile(dest)
	if err != nil {
		return fmt.Errorf("写出的 wintun.dll 无法读取: %w", err)
	}
	got := sha256.Sum256(written)
	if hex.EncodeToString(got[:]) != DLLSHA256 {
		_ = os.Remove(dest)
		return errors.New("写出的 wintun.dll 校验失败，拒绝启动")
	}
	return nil
}

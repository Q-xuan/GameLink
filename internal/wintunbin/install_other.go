//go:build !windows || !amd64

package wintunbin

import "errors"

// Install is a no-op off windows/amd64. A non-amd64 Windows build refuses
// to start rather than load another architecture's DLL.
func Install() error {
	if isWindows() && !isAMD64() {
		return errors.New("GameLink 只内置官方 Wintun 0.14.1 amd64，当前架构拒绝启动")
	}
	return nil
}

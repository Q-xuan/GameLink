//go:build windows

package tun

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// RequireAdmin fails when the process cannot create a Wintun adapter or
// change this interface's address and route.
func RequireAdmin() error {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil || !token.IsElevated() {
		if err == nil {
			token.Close()
		}
		return fmt.Errorf("需要管理员权限才能创建 GameLink 网卡、设置地址和路由。请右键以管理员身份运行")
	}
	token.Close()
	return nil
}

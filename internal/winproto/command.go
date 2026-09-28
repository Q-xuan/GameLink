// Package winproto registers the gamelink:// URL scheme for the current user.
package winproto

import "os"

// OpenCommand is the current-user shell command that opens this executable.
func OpenCommand(exe string) string {
	return `"` + exe + `" "%1"`
}

// RegisterCurrent writes HKCU\Software\Classes\gamelink for os.Executable.
// Non-Windows builds do nothing.
func RegisterCurrent() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return register(exe)
}

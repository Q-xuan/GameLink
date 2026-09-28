//go:build windows

package proxyhint

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConflictingProcess reports a known proxy TUN process, if one is running.
func ConflictingProcess() (string, bool) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return "", false
	}
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if matchProcess(name) {
			return name, true
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return "", false
}

// Present reports whether the window should offer the two DIRECT rules.
func Present() bool {
	if _, ok := ConflictingProcess(); ok {
		return true
	}
	return LocalFakeIP()
}

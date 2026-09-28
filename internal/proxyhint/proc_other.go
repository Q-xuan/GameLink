//go:build !windows

package proxyhint

// ConflictingProcess is only implemented on Windows.
func ConflictingProcess() (string, bool) { return "", false }

// Present reports a fake-ip address on non-Windows builds.
func Present() bool { return LocalFakeIP() }

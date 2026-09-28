//go:build !windows

package tun

// RequireAdmin is a no-op off Windows. Those builds do not install a driver,
// address, or route.
func RequireAdmin() error { return nil }

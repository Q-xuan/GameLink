// Package crypto provides the room-token HMAC used by the relay.
// The MAC is HMAC-SHA256 truncated to 16 bytes. The key is the 128-bit room token.
// It covers the 32-byte header and the payload, and does not cover itself.
package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
)

const Size = 16

// Sum returns the 16-byte truncated HMAC-SHA256 of msg under key.
func Sum(key, msg []byte) [Size]byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(msg)
	full := m.Sum(nil)
	var out [Size]byte
	copy(out[:], full[:Size])
	return out
}

// Verify reports whether mac matches Sum(key, msg).
func Verify(key, msg, mac []byte) bool {
	if len(mac) != Size {
		return false
	}
	sum := Sum(key, msg)
	return hmac.Equal(sum[:], mac)
}

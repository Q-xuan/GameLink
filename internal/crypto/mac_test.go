package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"testing"
)

func TestTruncatedHMAC(t *testing.T) {
	key := []byte("0123456789abcdef")
	msg := []byte("header-and-payload")
	got := Sum(key, msg)
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(msg)
	full := m.Sum(nil)
	if len(full) != sha256.Size {
		t.Fatal(len(full))
	}
	for i := 0; i < Size; i++ {
		if got[i] != full[i] {
			t.Fatalf("byte %d", i)
		}
	}
	if !Verify(key, msg, got[:]) {
		t.Fatal("verify")
	}
	bad := got
	bad[0] ^= 0xff
	if Verify(key, msg, bad[:]) {
		t.Fatal("accepted bad mac")
	}
	if Verify(key, msg, got[:Size-1]) {
		t.Fatal("accepted short mac")
	}
	other := Sum(key, []byte("other"))
	if other == got {
		t.Fatal("mac collision")
	}
}

package room

import (
	"encoding/hex"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/Q-xuan/GameLink/internal/peer"
)

func TestCreateJoinAndCapacity(t *testing.T) {
	s := NewStore()
	now := time.Unix(100, 0)
	info, host, err := s.Create(now)
	if err != nil {
		t.Fatal(err)
	}
	if host.ID != peer.HostID || host.VirtualIP.String() != "10.66.0.1" {
		t.Fatalf("host %+v", host)
	}
	if len(info.Code) != 6 {
		t.Fatal(info.Code)
	}
	if _, err := NormalizeCode(info.Code); err != nil {
		t.Fatal(err)
	}
	if hex.EncodedLen(len(info.Token)) != 32 {
		t.Fatal("token")
	}
	joined, p2, err := s.Join(stringsLower(info.Code), info.Token, now)
	if err != nil {
		t.Fatal(err)
	}
	if joined.ID != info.ID || p2.ID != 2 || p2.VirtualIP.String() != "10.66.0.2" {
		t.Fatalf("join %+v %+v", joined, p2)
	}
	if _, _, err := s.Join(info.Code, [16]byte{1}, now); !errors.Is(err, ErrToken) {
		t.Fatalf("bad token: %v", err)
	}
	if _, _, err := s.Join("ZZZZZZ", info.Token, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	for want := uint32(3); want <= peer.MaxPeers; want++ {
		_, p, err := s.Join(info.Code, info.Token, now)
		if err != nil {
			t.Fatal(err)
		}
		if p.ID != want || p.VirtualIP.String() != "10.66.0."+itoa(want) {
			t.Fatalf("peer %d got %d %s", want, p.ID, p.VirtualIP)
		}
	}
	if _, _, err := s.Join(info.Code, info.Token, now); !errors.Is(err, ErrFull) {
		t.Fatalf("full: %v", err)
	}
}

func TestRebindKeepsVirtualIP(t *testing.T) {
	s := NewStore()
	now := time.Now()
	info, host, err := s.Create(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Touch(info.ID, host.ID, mustAddr("127.0.0.1:1"), now); ok {
		t.Fatal("touch before handshake")
	}
	a := mustAddr("127.0.0.1:40000")
	b := mustAddr("127.0.0.1:40001")
	got, first, err := s.Bind(info.ID, host.ID, a, now)
	if err != nil || !first || got.Addr != a || got.VirtualIP.String() != "10.66.0.1" {
		t.Fatalf("bind %+v first=%v err=%v", got, first, err)
	}
	got, first, err = s.Bind(info.ID, host.ID, b, now.Add(time.Second))
	if err != nil || first || got.Addr != b || got.VirtualIP.String() != "10.66.0.1" {
		t.Fatalf("rebind %+v first=%v err=%v", got, first, err)
	}
	got, ok := s.Touch(info.ID, host.ID, mustAddr("10.1.2.3:9"), now.Add(2*time.Second))
	if !ok || got.Addr.String() != "10.1.2.3:9" || got.VirtualIP.String() != "10.66.0.1" {
		t.Fatalf("touch %+v", got)
	}
}

func TestExpireIdleAndReuseID(t *testing.T) {
	s := NewStore()
	now := time.Now()
	info, _, err := s.Create(now)
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := s.Join(info.Code, info.Token, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Bind(info.ID, p2.ID, mustAddr("127.0.0.1:2"), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Touch(info.ID, 1, mustAddr("127.0.0.1:1"), now); !ok {
		// host is not online yet; refresh via a direct bind so only peer 2 is idle
		if _, _, err := s.Bind(info.ID, 1, mustAddr("127.0.0.1:1"), now); err != nil {
			t.Fatal(err)
		}
	}
	gone := s.Expire(now, 20*time.Second)
	if len(gone) != 1 || gone[0].ID != p2.ID || !gone[0].Online() {
		t.Fatalf("gone %+v", gone)
	}
	if _, ok := s.Get(info.ID, p2.ID); ok {
		t.Fatal("peer 2 still present")
	}
	_, again, err := s.Join(info.Code, info.Token, now)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != p2.ID || again.VirtualIP.String() != "10.66.0.2" {
		t.Fatalf("reused %+v", again)
	}
	// Silence removes the remaining peer and the room.
	s.Expire(now.Add(21*time.Second), 20*time.Second)
	if _, ok := s.Token(info.ID); ok {
		t.Fatal("room survived")
	}
}

func TestParseToken(t *testing.T) {
	tok, err := ParseToken("00112233445566778899aabbccddeeff")
	if err != nil || tok[0] != 0x00 || tok[15] != 0xff {
		t.Fatal(err, tok)
	}
	if _, err := ParseToken("zz"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := NormalizeCode("ABC01"); err == nil {
		t.Fatal("short and ambiguous digits")
	}
}

func mustAddr(s string) netip.AddrPort {
	return netip.MustParseAddrPort(s)
}

func stringsLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

package peer

import "testing"

func TestVirtualIP(t *testing.T) {
	ip, ok := VirtualIP(HostID)
	if !ok || ip.String() != "10.66.0.1" {
		t.Fatalf("host vip %s ok=%v", ip, ok)
	}
	last, ok := VirtualIP(MaxPeers)
	if !ok || last.String() != "10.66.0.8" {
		t.Fatalf("last vip %s", last)
	}
	if _, ok := VirtualIP(0); ok {
		t.Fatal("id 0")
	}
	if _, ok := VirtualIP(MaxPeers + 1); ok {
		t.Fatal("id 9")
	}
}

package relay

import (
	"errors"
	"net/netip"
)

var errDrop = errors.New("drop")

// checkInnerIPv4 accepts a unicast IPv4 payload whose source equals vip.
// Broadcast and multicast destinations are dropped. Ports are not inspected.
func checkInnerIPv4(payload []byte, vip netip.Addr) error {
	if len(payload) < 20 || payload[0]>>4 != 4 {
		return errDrop
	}
	ihl := int(payload[0]&0x0f) * 4
	if ihl < 20 || len(payload) < ihl {
		return errDrop
	}
	var srcA, dstA [4]byte
	copy(srcA[:], payload[12:16])
	copy(dstA[:], payload[16:20])
	src := netip.AddrFrom4(srcA)
	dst := netip.AddrFrom4(dstA)
	if src != vip || dst.IsMulticast() || isBroadcast(dst) {
		return errDrop
	}
	return nil
}

func isBroadcast(dst netip.Addr) bool {
	if !dst.Is4() {
		return false
	}
	b := dst.As4()
	if b == [4]byte{255, 255, 255, 255} || b == [4]byte{10, 66, 0, 255} {
		return true
	}
	return false
}

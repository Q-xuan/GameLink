// Package tun moves raw IPv4 packets between a local adapter and the relay.
// Windows opens a Wintun adapter. Other systems use an in-memory device so
// the forwarder can be tested without a driver.
package tun

import (
	"net/netip"

	"github.com/Q-xuan/GameLink/internal/peer"
)

const (
	// AdapterName is the Windows interface name. internal/route uses the same name.
	AdapterName = "GameLink"
	// MTU is the only interface MTU the client installs.
	MTU = 1280
)

// Device is a layer-3 packet source. Read and Write transfer one IP packet.
type Device interface {
	Read() ([]byte, error)
	Write(pkt []byte) (int, error)
	Close() error
	MTU() int
}

// destPeer maps an outgoing unicast IPv4 packet onto a room peer.
// Non-IPv4, broadcast, multicast, and addresses outside 10.66.0.1–8 are dropped.
// Ports are not inspected.
func destPeer(pkt []byte, local netip.Addr) (uint32, bool) {
	dst, ok := ipv4UnicastDest(pkt)
	if !ok || dst == local {
		return 0, false
	}
	id, ok := peerID(dst)
	if !ok {
		return 0, false
	}
	return id, true
}

// deliverable reports whether an inbound packet may be written to the adapter.
func deliverable(pkt []byte) bool {
	_, ok := ipv4UnicastDest(pkt)
	return ok
}

func ipv4UnicastDest(pkt []byte) (netip.Addr, bool) {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return netip.Addr{}, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl {
		return netip.Addr{}, false
	}
	var dstb [4]byte
	copy(dstb[:], pkt[16:20])
	dst := netip.AddrFrom4(dstb)
	if !dst.IsValid() || dst.IsMulticast() || dst.IsUnspecified() || isBroadcast(dst) {
		return netip.Addr{}, false
	}
	return dst, true
}

func isBroadcast(dst netip.Addr) bool {
	if !dst.Is4() {
		return false
	}
	b := dst.As4()
	return b == [4]byte{255, 255, 255, 255} || b == [4]byte{10, 66, 0, 255}
}

func peerID(dst netip.Addr) (uint32, bool) {
	if !dst.Is4() {
		return 0, false
	}
	b := dst.As4()
	if b[0] != 10 || b[1] != 66 || b[2] != 0 {
		return 0, false
	}
	id := uint32(b[3])
	vip, ok := peer.VirtualIP(id)
	if !ok || vip != dst {
		return 0, false
	}
	return id, true
}

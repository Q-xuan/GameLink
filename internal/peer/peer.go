// Package peer describes a member of a GameLink room.
package peer

import (
	"net/netip"
	"time"
)

const (
	// MaxPeers is the room capacity. IDs are 1..MaxPeers.
	MaxPeers = 8
	// HostID is the peer created with the room. Its virtual address is 10.66.0.1.
	HostID = 1
)

// Peer is one UDP endpoint inside a room.
// VirtualIP is assigned at join time and does not change when Addr is rebound.
type Peer struct {
	ID        uint32
	RoomID    uint64
	VirtualIP netip.Addr
	Addr      netip.AddrPort
	LastSeen  time.Time
}

// Online reports whether a UDP source address has been bound.
func (p Peer) Online() bool {
	return p.Addr.IsValid()
}

// VirtualIP maps a peer ID onto 10.66.0.0/24.
// Peer 1 is 10.66.0.1. IDs outside 1..MaxPeers are rejected.
func VirtualIP(id uint32) (netip.Addr, bool) {
	if id < 1 || id > MaxPeers {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte{10, 66, 0, byte(id)}), true
}

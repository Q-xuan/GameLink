package main

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// WrapIPv4UDP builds an IPv4+UDP datagram. The relay checks the IPv4 source
// and drops broadcast or multicast; it does not look at the UDP ports.
func WrapIPv4UDP(src, dst netip.Addr, sport, dport uint16, payload []byte) ([]byte, error) {
	if !src.Is4() || !dst.Is4() {
		return nil, errors.New("need ipv4")
	}
	udpLen := 8 + len(payload)
	total := 20 + udpLen
	if total > 65535 {
		return nil, errors.New("packet too large")
	}
	b := make([]byte, total)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(total))
	b[8] = 64
	b[9] = 17
	s4 := src.As4()
	d4 := dst.As4()
	copy(b[12:16], s4[:])
	copy(b[16:20], d4[:])
	binary.BigEndian.PutUint16(b[10:12], ipChecksum(b[:20]))
	binary.BigEndian.PutUint16(b[20:22], sport)
	binary.BigEndian.PutUint16(b[22:24], dport)
	binary.BigEndian.PutUint16(b[24:26], uint16(udpLen))
	copy(b[28:], payload)
	return b, nil
}

// UnwrapIPv4UDP returns the inner UDP payload.
func UnwrapIPv4UDP(b []byte) (src, dst netip.Addr, sport, dport uint16, payload []byte, err error) {
	if len(b) < 28 || b[0]>>4 != 4 {
		return netip.Addr{}, netip.Addr{}, 0, 0, nil, errors.New("not ipv4")
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl+8 || b[9] != 17 {
		return netip.Addr{}, netip.Addr{}, 0, 0, nil, errors.New("not udp")
	}
	var s4, d4 [4]byte
	copy(s4[:], b[12:16])
	copy(d4[:], b[16:20])
	src = netip.AddrFrom4(s4)
	dst = netip.AddrFrom4(d4)
	sport = binary.BigEndian.Uint16(b[ihl : ihl+2])
	dport = binary.BigEndian.Uint16(b[ihl+2 : ihl+4])
	udpLen := int(binary.BigEndian.Uint16(b[ihl+4 : ihl+6]))
	if udpLen < 8 || ihl+udpLen > len(b) {
		return netip.Addr{}, netip.Addr{}, 0, 0, nil, errors.New("bad udp length")
	}
	payload = append([]byte(nil), b[ihl+8:ihl+udpLen]...)
	return src, dst, sport, dport, payload, nil
}

func ipChecksum(h []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(h); i += 2 {
		if i == 10 {
			continue
		}
		sum += uint32(binary.BigEndian.Uint16(h[i:]))
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

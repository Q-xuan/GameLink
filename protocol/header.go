// Package protocol encodes the 32-byte GameLink UDP header.
//
//	0  magic "GLNK"  4
//	4  version u8
//	5  type u8
//	6  flags u16
//	8  room_id u64
//	16 src_peer u32
//	20 dst_peer u32
//	24 sequence u32
//	28 payload_len u16
//	30 reserved u16
//	32 payload
//
// All multi-byte integers are big-endian. payload_len covers the payload
// only. When flags bit0 is set, a 16-byte MAC follows the payload and is
// not counted in payload_len. Sequence is statistics only.
package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	Magic      = "GLNK"
	Version    = 1
	HeaderSize = 32
	MACSize    = 16
	TokenSize  = 16

	TypeData      uint8 = 1
	TypePing      uint8 = 2
	TypePong      uint8 = 3
	TypeHandshake uint8 = 4

	// FlagMAC is bit0. When set, a truncated HMAC-SHA256 follows the payload.
	FlagMAC uint16 = 1 << 0
)

var (
	ErrShort    = errors.New("short packet")
	ErrMagic    = errors.New("bad magic")
	ErrVersion  = errors.New("bad version")
	ErrType     = errors.New("bad type")
	ErrPayload  = errors.New("bad payload length")
	ErrTrailing = errors.New("trailing bytes")
)

// Header is the fixed prefix of a GameLink datagram.
type Header struct {
	Version    uint8
	Type       uint8
	Flags      uint16
	RoomID     uint64
	SrcPeer    uint32
	DstPeer    uint32
	Sequence   uint32
	PayloadLen uint16
	Reserved   uint16
}

// MACPresent reports whether flags bit0 is set.
func MACPresent(flags uint16) bool {
	return flags&FlagMAC != 0
}

// Marshal encodes h and payload. PayloadLen is taken from len(payload).
func Marshal(h Header, payload []byte) ([]byte, error) {
	if h.Version != Version {
		return nil, ErrVersion
	}
	if h.Type == 0 {
		return nil, ErrType
	}
	if len(payload) > 65535 {
		return nil, ErrPayload
	}
	h.PayloadLen = uint16(len(payload))
	buf := make([]byte, HeaderSize+len(payload))
	copy(buf[0:4], Magic)
	buf[4] = h.Version
	buf[5] = h.Type
	binary.BigEndian.PutUint16(buf[6:8], h.Flags)
	binary.BigEndian.PutUint64(buf[8:16], h.RoomID)
	binary.BigEndian.PutUint32(buf[16:20], h.SrcPeer)
	binary.BigEndian.PutUint32(buf[20:24], h.DstPeer)
	binary.BigEndian.PutUint32(buf[24:28], h.Sequence)
	binary.BigEndian.PutUint16(buf[28:30], h.PayloadLen)
	binary.BigEndian.PutUint16(buf[30:32], h.Reserved)
	copy(buf[HeaderSize:], payload)
	return buf, nil
}

// Decode splits a datagram into header, payload, and optional MAC.
// The returned slices alias b.
func Decode(b []byte) (Header, []byte, []byte, error) {
	if len(b) < HeaderSize {
		return Header{}, nil, nil, ErrShort
	}
	if string(b[0:4]) != Magic {
		return Header{}, nil, nil, ErrMagic
	}
	h := Header{
		Version:    b[4],
		Type:       b[5],
		Flags:      binary.BigEndian.Uint16(b[6:8]),
		RoomID:     binary.BigEndian.Uint64(b[8:16]),
		SrcPeer:    binary.BigEndian.Uint32(b[16:20]),
		DstPeer:    binary.BigEndian.Uint32(b[20:24]),
		Sequence:   binary.BigEndian.Uint32(b[24:28]),
		PayloadLen: binary.BigEndian.Uint16(b[28:30]),
		Reserved:   binary.BigEndian.Uint16(b[30:32]),
	}
	if h.Version != Version {
		return h, nil, nil, ErrVersion
	}
	need := HeaderSize + int(h.PayloadLen)
	macN := 0
	if MACPresent(h.Flags) {
		macN = MACSize
	}
	if len(b) < need+macN {
		return h, nil, nil, ErrShort
	}
	if len(b) != need+macN {
		return h, nil, nil, ErrTrailing
	}
	payload := b[HeaderSize:need]
	var mac []byte
	if macN > 0 {
		mac = b[need : need+macN]
	}
	return h, payload, mac, nil
}

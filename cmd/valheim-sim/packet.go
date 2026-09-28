package main

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	simMagic  = "VSIM"
	headerLen = 25
	MinSize   = 64
	MaxSize   = 1200
	KindData  = 1
	KindAck   = 2
)

// Packet is the Valheim simulator datagram carried as a UDP payload.
type Packet struct {
	SessionID uint32
	Sequence  uint32
	Timestamp int64
	AckSeq    uint32
	Kind      uint8
}

// Encode writes p and pads with zeros out to size. size must be 64..1200.
func Encode(p Packet, size int) ([]byte, error) {
	if size < MinSize || size > MaxSize {
		return nil, fmt.Errorf("size %d outside %d-%d", size, MinSize, MaxSize)
	}
	buf := make([]byte, size)
	copy(buf[0:4], simMagic)
	binary.BigEndian.PutUint32(buf[4:8], p.SessionID)
	binary.BigEndian.PutUint32(buf[8:12], p.Sequence)
	binary.BigEndian.PutUint64(buf[12:20], uint64(p.Timestamp))
	binary.BigEndian.PutUint32(buf[20:24], p.AckSeq)
	buf[24] = p.Kind
	return buf, nil
}

// Decode parses a simulator datagram.
func Decode(b []byte) (Packet, error) {
	if len(b) < headerLen || len(b) > MaxSize {
		return Packet{}, errors.New("bad sim size")
	}
	if string(b[0:4]) != simMagic {
		return Packet{}, errors.New("bad sim magic")
	}
	return Packet{
		SessionID: binary.BigEndian.Uint32(b[4:8]),
		Sequence:  binary.BigEndian.Uint32(b[8:12]),
		Timestamp: int64(binary.BigEndian.Uint64(b[12:20])),
		AckSeq:    binary.BigEndian.Uint32(b[20:24]),
		Kind:      b[24],
	}, nil
}

package relay

import (
	"errors"

	"github.com/Q-xuan/GameLink/internal/crypto"
	"github.com/Q-xuan/GameLink/protocol"
)

var (
	ErrBadMAC      = errors.New("bad mac")
	ErrMACRequired = errors.New("mac required")
)

// Seal appends a 16-byte truncated HMAC over header and payload.
func Seal(token [protocol.TokenSize]byte, h protocol.Header, payload []byte) ([]byte, error) {
	h.Flags |= protocol.FlagMAC
	buf, err := protocol.Marshal(h, payload)
	if err != nil {
		return nil, err
	}
	mac := crypto.Sum(token[:], buf)
	return append(buf, mac[:]...), nil
}

// Authenticate checks the MAC policy and returns the header and payload.
func Authenticate(token [protocol.TokenSize]byte, buf []byte, requireMAC bool) (protocol.Header, []byte, error) {
	h, payload, mac, err := protocol.Decode(buf)
	if err != nil {
		return protocol.Header{}, nil, err
	}
	if protocol.MACPresent(h.Flags) {
		covered := buf[:protocol.HeaderSize+len(payload)]
		if !crypto.Verify(token[:], covered, mac) {
			return protocol.Header{}, nil, ErrBadMAC
		}
	} else if requireMAC {
		return protocol.Header{}, nil, ErrMACRequired
	}
	return h, payload, nil
}

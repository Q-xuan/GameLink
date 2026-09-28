package relay

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/Q-xuan/GameLink/protocol"
)

// Session is one client's UDP association with the relay.
type Session struct {
	conn      *net.UDPConn
	roomID    uint64
	peerID    uint32
	token     [protocol.TokenSize]byte
	insecure  bool
	seq       atomic.Uint32
	pingEvery time.Duration
}

// SessionConfig identifies the room membership used on the wire.
type SessionConfig struct {
	Relay        string
	Local        string // optional local UDP address; empty lets the kernel choose
	RoomID       uint64
	PeerID       uint32
	Token        [protocol.TokenSize]byte
	Insecure     bool
	PingInterval time.Duration
}

// Dial opens a connected UDP socket to the relay.
// cfg.Relay is used as a literal address when it has no hostname.
func Dial(cfg SessionConfig) (*Session, error) {
	raddr, err := net.ResolveUDPAddr("udp4", cfg.Relay)
	if err != nil {
		return nil, err
	}
	var laddr *net.UDPAddr
	if cfg.Local != "" {
		laddr, err = net.ResolveUDPAddr("udp4", cfg.Local)
		if err != nil {
			return nil, err
		}
	}
	conn, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		return nil, err
	}
	every := cfg.PingInterval
	if every <= 0 {
		every = PingInterval
	}
	return &Session{
		conn:      conn,
		roomID:    cfg.RoomID,
		peerID:    cfg.PeerID,
		token:     cfg.Token,
		insecure:  cfg.Insecure,
		pingEvery: every,
	}, nil
}

func (s *Session) Close() error {
	return s.conn.Close()
}

func (s *Session) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

// Send writes one datagram. Sequence is a statistic, not an anti-replay counter.
func (s *Session) Send(typ uint8, dst uint32, payload []byte) error {
	h := protocol.Header{
		Version:  protocol.Version,
		Type:     typ,
		RoomID:   s.roomID,
		SrcPeer:  s.peerID,
		DstPeer:  dst,
		Sequence: s.seq.Add(1),
	}
	var buf []byte
	var err error
	if s.insecure {
		buf, err = protocol.Marshal(h, payload)
	} else {
		buf, err = Seal(s.token, h, payload)
	}
	if err != nil {
		return err
	}
	_, err = s.conn.Write(buf)
	return err
}

// Handshake binds this socket's source address to the peer ID.
func (s *Session) Handshake(ctx context.Context) error {
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	payload := append([]byte(nil), s.token[:]...)
	nextSend := time.Time{}
	buf := make([]byte, 2048)
	for {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return errors.New("handshake timeout")
		}
		if !time.Now().Before(nextSend) {
			if err := s.Send(protocol.TypeHandshake, 0, payload); err != nil {
				return err
			}
			nextSend = time.Now().Add(200 * time.Millisecond)
		}
		wait := time.Until(nextSend)
		if left := time.Until(deadline); left < wait {
			wait = left
		}
		if wait < 0 {
			wait = 0
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(wait))
		n, err := s.conn.Read(buf)
		if err != nil {
			continue
		}
		h, _, err := Authenticate(s.token, buf[:n], !s.insecure)
		if err != nil {
			continue
		}
		if h.Type == protocol.TypeHandshake && h.DstPeer == s.peerID && h.RoomID == s.roomID {
			return nil
		}
	}
}

// Keepalive sends Ping immediately and then every PingInterval.
func (s *Session) Keepalive(ctx context.Context) error {
	if err := s.Send(protocol.TypePing, 0, nil); err != nil {
		return err
	}
	t := time.NewTicker(s.pingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.Send(protocol.TypePing, 0, nil); err != nil {
				return err
			}
		}
	}
}

// Recv blocks until a packet for this peer arrives or ctx ends.
// Only one Recv may run at a time.
func (s *Session) Recv(ctx context.Context) (protocol.Header, []byte, error) {
	buf := make([]byte, 65535)
	for {
		if err := ctx.Err(); err != nil {
			return protocol.Header{}, nil, err
		}
		dl := time.Now().Add(200 * time.Millisecond)
		if d, ok := ctx.Deadline(); ok && d.Before(dl) {
			dl = d
		}
		_ = s.conn.SetReadDeadline(dl)
		n, err := s.conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return protocol.Header{}, nil, err
		}
		h, payload, err := Authenticate(s.token, buf[:n], !s.insecure)
		if err != nil || h.RoomID != s.roomID || h.DstPeer != s.peerID {
			continue
		}
		return h, append([]byte(nil), payload...), nil
	}
}

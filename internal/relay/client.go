package relay

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Q-xuan/GameLink/protocol"
)

// ErrHandshakeTimeout means the relay did not answer before the deadline.
// Callers that set a context deadline wait until that deadline. Otherwise
// the wait is 3 seconds, which is what the CLI uses.
var ErrHandshakeTimeout = errors.New("handshake timeout")

// Session is one client's UDP association with the relay.
type Session struct {
	conn      *net.UDPConn
	roomID    uint64
	peerID    uint32
	token     [protocol.TokenSize]byte
	insecure  bool
	seq       atomic.Uint32
	pingEvery time.Duration
	probe     probeState
}

// Probe is the latest Ping/Pong observation on this session.
type Probe struct {
	LastPing time.Time
	LastPong time.Time
	RTT      time.Duration
	HasRTT   bool
	Waiting  bool
}

type probeState struct {
	mu       sync.Mutex
	pings    map[uint32]time.Time
	lastPing time.Time
	lastPong time.Time
	rtt      time.Duration
	hasRTT   bool
	waiting  bool
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
	if err == nil && typ == protocol.TypePing {
		s.notePing(h.Sequence)
	}
	return err
}

// Probe returns the latest Ping/Pong round trip.
func (s *Session) Probe() Probe {
	s.probe.mu.Lock()
	defer s.probe.mu.Unlock()
	return Probe{
		LastPing: s.probe.lastPing,
		LastPong: s.probe.lastPong,
		RTT:      s.probe.rtt,
		HasRTT:   s.probe.hasRTT,
		Waiting:  s.probe.waiting,
	}
}

func (s *Session) notePing(seq uint32) {
	s.probe.mu.Lock()
	defer s.probe.mu.Unlock()
	if s.probe.pings == nil {
		s.probe.pings = make(map[uint32]time.Time)
	}
	now := time.Now()
	s.probe.pings[seq] = now
	s.probe.lastPing = now
	s.probe.waiting = true
	for len(s.probe.pings) > 8 {
		var oldest uint32
		var when time.Time
		first := true
		for id, at := range s.probe.pings {
			if first || at.Before(when) {
				oldest, when, first = id, at, false
			}
		}
		delete(s.probe.pings, oldest)
	}
}

func (s *Session) notePong(seq uint32) {
	s.probe.mu.Lock()
	defer s.probe.mu.Unlock()
	now := time.Now()
	s.probe.lastPong = now
	s.probe.waiting = false
	if at, ok := s.probe.pings[seq]; ok {
		delete(s.probe.pings, seq)
		s.probe.rtt = now.Sub(at)
		s.probe.hasRTT = true
	}
}

func handshakeDeadline(now time.Time, ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return now.Add(3 * time.Second)
}

// Handshake binds this socket's source address to the peer ID.
func (s *Session) Handshake(ctx context.Context) error {
	deadline := handshakeDeadline(time.Now(), ctx)
	payload := append([]byte(nil), s.token[:]...)
	nextSend := time.Time{}
	buf := make([]byte, 2048)
	for {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return ErrHandshakeTimeout
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
		if h.Type == protocol.TypePong {
			s.notePong(h.Sequence)
		}
		return h, append([]byte(nil), payload...), nil
	}
}

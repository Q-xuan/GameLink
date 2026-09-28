package relay

import (
	"context"
	"crypto/subtle"
	"log"
	"net"
	"net/netip"
	"time"

	"github.com/Q-xuan/GameLink/internal/peer"
	"github.com/Q-xuan/GameLink/internal/room"
	"github.com/Q-xuan/GameLink/protocol"
)

const (
	// PingInterval is how often a client refreshes its UDP mapping.
	PingInterval = 5 * time.Second
	// IdleTimeout removes a peer after this much silence.
	IdleTimeout = 20 * time.Second
)

// Observer receives presence changes. WebSocket fan-out implements it.
type Observer interface {
	PeerUp(p peer.Peer)
	PeerDown(p peer.Peer)
}

// Options configures the UDP forwarder.
// MAC is required unless Insecure is set.
type Options struct {
	Rooms       *room.Store
	Observer    Observer
	Insecure    bool
	IdleTimeout time.Duration
	SweepEvery  time.Duration
	Logger      *log.Logger
}

// Server forwards authenticated datagrams between peers in a room.
type Server struct {
	rooms      *room.Store
	obs        Observer
	requireMAC bool
	idle       time.Duration
	sweepEvery time.Duration
	log        *log.Logger
}

func New(opt Options) *Server {
	idle := opt.IdleTimeout
	if idle <= 0 {
		idle = IdleTimeout
	}
	sweep := opt.SweepEvery
	if sweep <= 0 {
		sweep = time.Second
	}
	return &Server{
		rooms:      opt.Rooms,
		obs:        opt.Observer,
		requireMAC: !opt.Insecure,
		idle:       idle,
		sweepEvery: sweep,
		log:        opt.Logger,
	}
}

// Serve reads conn until ctx is cancelled or conn is closed.
// The UDP source address of each datagram is the NAT mapping. HTTP headers are ignored.
func (s *Server) Serve(ctx context.Context, conn *net.UDPConn) error {
	go s.sweep(ctx)
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		pkt := append([]byte(nil), buf[:n]...)
		s.handle(conn, addr, pkt)
	}
}

func (s *Server) handle(conn *net.UDPConn, src netip.AddrPort, buf []byte) {
	peek, _, _, err := protocol.Decode(buf)
	if err != nil {
		return
	}
	token, ok := s.rooms.Token(peek.RoomID)
	if !ok {
		return
	}
	h, payload, err := Authenticate(token, buf, s.requireMAC)
	if err != nil {
		return
	}
	now := time.Now()
	switch h.Type {
	case protocol.TypeHandshake:
		if len(payload) != protocol.TokenSize || subtle.ConstantTimeCompare(payload, token[:]) != 1 {
			return
		}
		p, first, err := s.rooms.Bind(h.RoomID, h.SrcPeer, src, now)
		if err != nil {
			return
		}
		if first && s.obs != nil {
			s.obs.PeerUp(p)
			s.logf("handshake room=%d peer=%d vip=%s addr=%s", p.RoomID, p.ID, p.VirtualIP, p.Addr)
		}
		s.reply(conn, src, token, h, protocol.TypeHandshake, nil)
	case protocol.TypePing:
		if _, ok := s.rooms.Touch(h.RoomID, h.SrcPeer, src, now); !ok {
			return
		}
		s.reply(conn, src, token, h, protocol.TypePong, nil)
	case protocol.TypeData:
		p, ok := s.rooms.Get(h.RoomID, h.SrcPeer)
		if !ok || !p.Online() {
			return
		}
		if err := checkInnerIPv4(payload, p.VirtualIP); err != nil {
			return
		}
		if _, ok := s.rooms.Touch(h.RoomID, h.SrcPeer, src, now); !ok {
			return
		}
		if h.DstPeer == 0 || h.DstPeer == h.SrcPeer {
			return
		}
		dst, ok := s.rooms.Endpoint(h.RoomID, h.DstPeer)
		if !ok {
			return
		}
		_, _ = conn.WriteToUDPAddrPort(buf, dst)
	}
}

func (s *Server) reply(conn *net.UDPConn, dst netip.AddrPort, token [protocol.TokenSize]byte, in protocol.Header, typ uint8, payload []byte) {
	h := protocol.Header{
		Version:  protocol.Version,
		Type:     typ,
		RoomID:   in.RoomID,
		SrcPeer:  0,
		DstPeer:  in.SrcPeer,
		Sequence: in.Sequence,
	}
	var buf []byte
	var err error
	if s.requireMAC || protocol.MACPresent(in.Flags) {
		buf, err = Seal(token, h, payload)
	} else {
		buf, err = protocol.Marshal(h, payload)
	}
	if err != nil {
		return
	}
	_, _ = conn.WriteToUDPAddrPort(buf, dst)
}

func (s *Server) sweep(ctx context.Context) {
	t := time.NewTicker(s.sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, p := range s.rooms.Expire(now, s.idle) {
				s.logf("expire room=%d peer=%d vip=%s", p.RoomID, p.ID, p.VirtualIP)
				if s.obs != nil {
					s.obs.PeerDown(p)
				}
			}
		}
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Printf(format, args...)
}

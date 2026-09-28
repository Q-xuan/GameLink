// Package room keeps GameLink rooms in memory. A restart drops them.
package room

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Q-xuan/GameLink/internal/peer"
	"github.com/Q-xuan/GameLink/protocol"
)

const (
	codeLen  = 6
	alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var (
	ErrNotFound = errors.New("room not found")
	ErrToken    = errors.New("invalid token")
	ErrFull     = errors.New("room full")
	ErrPeer     = errors.New("peer not in room")
	ErrCode     = errors.New("invalid room code")
)

// Info is a copy of the room identity returned to the control plane.
type Info struct {
	ID    uint64
	Code  string
	Token [protocol.TokenSize]byte
}

type state struct {
	id    uint64
	code  string
	token [protocol.TokenSize]byte
	peers map[uint32]*peer.Peer
}

// Store is the in-memory room table.
type Store struct {
	mu     sync.Mutex
	byID   map[uint64]*state
	byCode map[string]*state
}

func NewStore() *Store {
	return &Store{
		byID:   map[uint64]*state{},
		byCode: map[string]*state{},
	}
}

// Create opens a room and assigns peer 1 / 10.66.0.1.
func (s *Store) Create(now time.Time) (Info, peer.Peer, error) {
	for attempt := 0; attempt < 8; attempt++ {
		token, err := randomToken()
		if err != nil {
			return Info{}, peer.Peer{}, err
		}
		id, err := randomID()
		if err != nil {
			return Info{}, peer.Peer{}, err
		}
		code, err := randomCode()
		if err != nil {
			return Info{}, peer.Peer{}, err
		}
		vip, _ := peer.VirtualIP(peer.HostID)
		p := &peer.Peer{
			ID:        peer.HostID,
			RoomID:    id,
			VirtualIP: vip,
			LastSeen:  now,
		}
		s.mu.Lock()
		if _, exists := s.byID[id]; exists {
			s.mu.Unlock()
			continue
		}
		if _, exists := s.byCode[code]; exists {
			s.mu.Unlock()
			continue
		}
		rm := &state{
			id:    id,
			code:  code,
			token: token,
			peers: map[uint32]*peer.Peer{p.ID: p},
		}
		s.byID[id] = rm
		s.byCode[code] = rm
		s.mu.Unlock()
		return Info{ID: id, Code: code, Token: token}, snap(p), nil
	}
	return Info{}, peer.Peer{}, errors.New("could not allocate room")
}

// Join adds a peer to an existing room. The caller must present the room token.
func (s *Store) Join(code string, token [protocol.TokenSize]byte, now time.Time) (Info, peer.Peer, error) {
	norm, err := NormalizeCode(code)
	if err != nil {
		return Info{}, peer.Peer{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.byCode[norm]
	if !ok {
		return Info{}, peer.Peer{}, ErrNotFound
	}
	if subtle.ConstantTimeCompare(token[:], rm.token[:]) != 1 {
		return Info{}, peer.Peer{}, ErrToken
	}
	id, ok := allocID(rm.peers)
	if !ok {
		return Info{}, peer.Peer{}, ErrFull
	}
	vip, _ := peer.VirtualIP(id)
	p := &peer.Peer{
		ID:        id,
		RoomID:    rm.id,
		VirtualIP: vip,
		LastSeen:  now,
	}
	rm.peers[id] = p
	return Info{ID: rm.id, Code: rm.code, Token: rm.token}, snap(p), nil
}

// Authorize checks a websocket or API caller against the room token and membership.
func (s *Store) Authorize(code string, token [protocol.TokenSize]byte, peerID uint32) (uint64, error) {
	norm, err := NormalizeCode(code)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.byCode[norm]
	if !ok {
		return 0, ErrNotFound
	}
	if subtle.ConstantTimeCompare(token[:], rm.token[:]) != 1 {
		return 0, ErrToken
	}
	if _, ok := rm.peers[peerID]; !ok {
		return 0, ErrPeer
	}
	return rm.id, nil
}

// Token returns a copy of the room key.
func (s *Store) Token(roomID uint64) ([protocol.TokenSize]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.byID[roomID]
	if !ok {
		return [protocol.TokenSize]byte{}, false
	}
	return rm.token, true
}

// Get returns a snapshot of one peer.
func (s *Store) Get(roomID uint64, peerID uint32) (peer.Peer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.byID[roomID]
	if !ok {
		return peer.Peer{}, false
	}
	p, ok := rm.peers[peerID]
	if !ok {
		return peer.Peer{}, false
	}
	return snap(p), true
}

// Peers returns snapshots of every member, unsorted.
func (s *Store) Peers(roomID uint64) []peer.Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.byID[roomID]
	if !ok {
		return nil
	}
	out := make([]peer.Peer, 0, len(rm.peers))
	for _, p := range rm.peers {
		out = append(out, snap(p))
	}
	return out
}

// Bind attaches a UDP source address to a peer. The virtual IP stays.
// The bool is true when this is the first address seen for the peer.
func (s *Store) Bind(roomID uint64, peerID uint32, addr netip.AddrPort, now time.Time) (peer.Peer, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.byID[roomID]
	if !ok {
		return peer.Peer{}, false, ErrNotFound
	}
	p, ok := rm.peers[peerID]
	if !ok {
		return peer.Peer{}, false, ErrPeer
	}
	first := !p.Addr.IsValid()
	p.Addr = addr
	p.LastSeen = now
	return snap(p), first, nil
}

// Touch refreshes LastSeen and rebinds Addr when the UDP source changes.
// It fails until Bind has succeeded once.
func (s *Store) Touch(roomID uint64, peerID uint32, addr netip.AddrPort, now time.Time) (peer.Peer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.byID[roomID]
	if !ok {
		return peer.Peer{}, false
	}
	p, ok := rm.peers[peerID]
	if !ok || !p.Addr.IsValid() {
		return peer.Peer{}, false
	}
	if addr.IsValid() {
		p.Addr = addr
	}
	p.LastSeen = now
	return snap(p), true
}

// Endpoint is the current UDP address of an online peer.
func (s *Store) Endpoint(roomID uint64, peerID uint32) (netip.AddrPort, bool) {
	p, ok := s.Get(roomID, peerID)
	if !ok || !p.Online() {
		return netip.AddrPort{}, false
	}
	return p.Addr, true
}

// Expire removes peers silent for at least idle and deletes empty rooms.
func (s *Store) Expire(now time.Time, idle time.Duration) []peer.Peer {
	if idle <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []peer.Peer
	var empty []uint64
	for id, rm := range s.byID {
		var drop []uint32
		for pid, p := range rm.peers {
			if now.Sub(p.LastSeen) >= idle {
				gone = append(gone, snap(p))
				drop = append(drop, pid)
			}
		}
		for _, pid := range drop {
			delete(rm.peers, pid)
		}
		if len(rm.peers) == 0 {
			empty = append(empty, id)
		}
	}
	for _, id := range empty {
		rm := s.byID[id]
		delete(s.byCode, rm.code)
		delete(s.byID, id)
	}
	return gone
}

// ParseToken decodes a 128-bit hex room token.
func ParseToken(s string) ([protocol.TokenSize]byte, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != protocol.TokenSize {
		return [protocol.TokenSize]byte{}, ErrToken
	}
	var out [protocol.TokenSize]byte
	copy(out[:], raw)
	return out, nil
}

// NormalizeCode checks a short human room code.
func NormalizeCode(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != codeLen {
		return "", ErrCode
	}
	for _, c := range code {
		if !strings.ContainsRune(alphabet, c) {
			return "", ErrCode
		}
	}
	return code, nil
}

func snap(p *peer.Peer) peer.Peer {
	return *p
}

func allocID(peers map[uint32]*peer.Peer) (uint32, bool) {
	for id := uint32(1); id <= peer.MaxPeers; id++ {
		if _, ok := peers[id]; !ok {
			return id, true
		}
	}
	return 0, false
}

func randomToken() ([protocol.TokenSize]byte, error) {
	var t [protocol.TokenSize]byte
	_, err := rand.Read(t[:])
	return t, err
}

func randomID() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	id := binary.BigEndian.Uint64(b[:])
	if id == 0 {
		id = 1
	}
	return id, nil
}

func randomCode() (string, error) {
	buf := make([]byte, codeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, codeLen)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

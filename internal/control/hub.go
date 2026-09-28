package control

import (
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Q-xuan/GameLink/internal/peer"
	"github.com/Q-xuan/GameLink/internal/room"
)

// Event is a presence message pushed after join.
type Event struct {
	Type   string     `json:"type"`
	Peers  []PeerInfo `json:"peers,omitempty"`
	PeerID uint32     `json:"peer_id,omitempty"`
	VIP    string     `json:"vip,omitempty"`
	Online *bool      `json:"online,omitempty"`
}

// PeerInfo is one row in a presence snapshot.
type PeerInfo struct {
	PeerID uint32 `json:"peer_id"`
	VIP    string `json:"vip"`
	Online bool   `json:"online"`
}

type subscriber struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *subscriber) write(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return s.conn.WriteJSON(v)
}

// Hub fans presence changes out to websocket subscribers of a room.
type Hub struct {
	rooms *room.Store
	mu    sync.Mutex
	subs  map[uint64]map[*subscriber]struct{}
}

func NewHub(rooms *room.Store) *Hub {
	return &Hub{rooms: rooms, subs: map[uint64]map[*subscriber]struct{}{}}
}

// Serve sends a snapshot and then blocks until the socket closes.
func (h *Hub) Serve(roomID uint64, conn *websocket.Conn) {
	sub := &subscriber{conn: conn}
	h.mu.Lock()
	if h.subs[roomID] == nil {
		h.subs[roomID] = map[*subscriber]struct{}{}
	}
	h.subs[roomID][sub] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if set := h.subs[roomID]; set != nil {
			delete(set, sub)
		}
		h.mu.Unlock()
		_ = conn.Close()
	}()
	_ = sub.write(h.snapshot(roomID))
	conn.SetReadLimit(1024)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// PeerUp implements relay.Observer.
func (h *Hub) PeerUp(p peer.Peer) {
	online := true
	h.broadcast(p.RoomID, Event{
		Type:   "peer_online",
		PeerID: p.ID,
		VIP:    p.VirtualIP.String(),
		Online: &online,
	})
}

// PeerDown implements relay.Observer.
func (h *Hub) PeerDown(p peer.Peer) {
	online := false
	h.broadcast(p.RoomID, Event{
		Type:   "peer_offline",
		PeerID: p.ID,
		VIP:    p.VirtualIP.String(),
		Online: &online,
	})
}

// Close drops every subscriber.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.subs {
		for sub := range set {
			_ = sub.conn.Close()
		}
	}
}

func (h *Hub) broadcast(roomID uint64, ev Event) {
	h.mu.Lock()
	set := h.subs[roomID]
	list := make([]*subscriber, 0, len(set))
	for sub := range set {
		list = append(list, sub)
	}
	h.mu.Unlock()
	var dead []*subscriber
	for _, sub := range list {
		if err := sub.write(ev); err != nil {
			dead = append(dead, sub)
		}
	}
	if len(dead) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	set = h.subs[roomID]
	for _, sub := range dead {
		delete(set, sub)
	}
}

func (h *Hub) snapshot(roomID uint64) Event {
	peers := h.rooms.Peers(roomID)
	infos := make([]PeerInfo, 0, len(peers))
	for _, p := range peers {
		infos = append(infos, PeerInfo{
			PeerID: p.ID,
			VIP:    p.VirtualIP.String(),
			Online: p.Online(),
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].PeerID < infos[j].PeerID })
	return Event{Type: "snapshot", Peers: infos}
}

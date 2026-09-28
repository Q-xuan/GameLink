package control

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Q-xuan/GameLink/internal/room"
)

// Created is the body of POST /v1/rooms.
type Created struct {
	Code       string `json:"code"`
	Token      string `json:"token"`
	RoomID     string `json:"room_id"`
	PeerID     uint32 `json:"peer_id"`
	VIP        string `json:"vip"`
	Relay      string `json:"relay"`
	ControlURL string `json:"control_url"`
}

// Joined is the body of POST /v1/rooms/{code}/join.
type Joined struct {
	Code       string `json:"code"`
	RoomID     string `json:"room_id"`
	PeerID     uint32 `json:"peer_id"`
	VIP        string `json:"vip"`
	Relay      string `json:"relay"`
	ControlURL string `json:"control_url"`
}

type joinRequest struct {
	Token string `json:"token"`
}

// Options configures the HTTP and websocket front door.
type Options struct {
	Rooms            *room.Store
	Hub              *Hub
	PublicRelay      string
	PublicControlURL string
	Logger           *log.Logger
}

// Server is the plain HTTP control plane. It does not terminate TLS.
type Server struct {
	rooms         *room.Store
	hub           *Hub
	publicRelay   string
	publicControl string
	log           *log.Logger
	upgrader      websocket.Upgrader
}

func New(opt Options) *Server {
	return &Server{
		rooms:         opt.Rooms,
		hub:           opt.Hub,
		publicRelay:   opt.PublicRelay,
		publicControl: opt.PublicControlURL,
		log:           opt.Logger,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// Handler routes health, room create/join, and the presence websocket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /v1/rooms", s.create)
	mux.HandleFunc("POST /v1/rooms/{code}/join", s.join)
	mux.HandleFunc("GET /v1/rooms/{code}/ws", s.events)
	return mux
}

// Serve accepts on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          s.logger(),
	}
	go func() {
		<-ctx.Done()
		if s.hub != nil {
			s.hub.Close()
		}
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		_ = ln.Close()
	}()
	err := srv.Serve(ln)
	if ctx.Err() != nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	info, p, err := s.rooms.Create(time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	// X-Forwarded-For is logged only. It is not a UDP source address.
	s.logf("room create code=%s peer=%d remote=%s xff=%q", info.Code, p.ID, r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
	writeJSON(w, http.StatusCreated, Created{
		Code:       info.Code,
		Token:      hex.EncodeToString(info.Token[:]),
		RoomID:     strconv.FormatUint(info.ID, 10),
		PeerID:     p.ID,
		VIP:        p.VirtualIP.String(),
		Relay:      s.publicRelay,
		ControlURL: s.publicControl,
	})
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	code, err := room.NormalizeCode(r.PathValue("code"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid room code")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	token, err := room.ParseToken(req.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token")
		return
	}
	info, p, err := s.rooms.Join(code, token, time.Now())
	if err != nil {
		writeJoinError(w, err)
		return
	}
	s.logf("room join code=%s peer=%d vip=%s remote=%s xff=%q", info.Code, p.ID, p.VirtualIP, r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
	writeJSON(w, http.StatusOK, Joined{
		Code:       info.Code,
		RoomID:     strconv.FormatUint(info.ID, 10),
		PeerID:     p.ID,
		VIP:        p.VirtualIP.String(),
		Relay:      s.publicRelay,
		ControlURL: s.publicControl,
	})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	token, err := room.ParseToken(r.URL.Query().Get("token"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token")
		return
	}
	id64, err := strconv.ParseUint(r.URL.Query().Get("peer_id"), 10, 32)
	if err != nil || id64 == 0 {
		writeError(w, http.StatusBadRequest, "invalid peer_id")
		return
	}
	roomID, err := s.rooms.Authorize(r.PathValue("code"), token, uint32(id64))
	if err != nil {
		writeJoinError(w, err)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.Serve(roomID, conn)
}

func writeJoinError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, room.ErrNotFound):
		writeError(w, http.StatusNotFound, "room not found")
	case errors.Is(err, room.ErrToken), errors.Is(err, room.ErrPeer):
		writeError(w, http.StatusUnauthorized, "invalid token")
	case errors.Is(err, room.ErrFull):
		writeError(w, http.StatusConflict, "room full")
	case errors.Is(err, room.ErrCode):
		writeError(w, http.StatusBadRequest, "invalid room code")
	default:
		writeError(w, http.StatusBadRequest, "rejected")
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) logf(format string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Printf(format, args...)
}

func (s *Server) logger() *log.Logger {
	if s.log != nil {
		return s.log
	}
	return log.New(io.Discard, "", 0)
}

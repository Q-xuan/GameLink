package relay

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Q-xuan/GameLink/internal/control"
	"github.com/Q-xuan/GameLink/internal/room"
	"github.com/Q-xuan/GameLink/protocol"
)

func TestSessionWhenControlAndUDPAddressesDiffer(t *testing.T) {
	rooms := room.NewStore()
	uc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = New(Options{Rooms: rooms}).Serve(ctx, uc) }()

	var httpRemote string
	ctrl := control.New(control.Options{
		Rooms:            rooms,
		Hub:              control.NewHub(rooms),
		PublicRelay:      uc.LocalAddr().String(),
		PublicControlURL: "http://127.0.0.1",
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/rooms" {
			httpRemote = r.RemoteAddr
		}
		ctrl.Handler().ServeHTTP(w, r)
	}))
	defer ts.Close()

	created, err := control.CreateRoom(context.Background(), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	httpIP, _, err := net.SplitHostPort(httpRemote)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := control.JoinRoom(context.Background(), ts.URL, created.Code, created.Token)
	if err != nil {
		t.Fatal(err)
	}
	token, err := room.ParseToken(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	roomID, err := strconv.ParseUint(created.RoomID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}

	s1 := dialFrom(t, "127.0.0.2:0", uc.LocalAddr().String(), roomID, created.PeerID, token)
	s2 := dialFrom(t, "127.0.0.2:0", uc.LocalAddr().String(), roomID, joined.PeerID, token)
	udpIP := s1.LocalAddr().(*net.UDPAddr).IP.String()
	if httpIP == udpIP {
		t.Fatalf("control remote %s and udp source %s were not separated", httpRemote, s1.LocalAddr())
	}
	before, ok := rooms.Get(roomID, created.PeerID)
	if !ok || before.Online() {
		t.Fatal("peer bound before handshake")
	}

	hctx, hcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer hcancel()
	if err := s1.Handshake(hctx); err != nil {
		t.Fatal(err)
	}
	if err := s2.Handshake(hctx); err != nil {
		t.Fatal(err)
	}
	assertUDPEndpoint(t, rooms, roomID, created.PeerID, s1.LocalAddr().(*net.UDPAddr), httpIP)
	assertUDPEndpoint(t, rooms, roomID, joined.PeerID, s2.LocalAddr().(*net.UDPAddr), httpIP)

	out := ipv4Packet([4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{1})
	if err := s1.Send(protocol.TypeData, joined.PeerID, out); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer rcancel()
	if _, got, err := s2.Recv(rctx); err != nil || string(got) != string(out) {
		t.Fatalf("forward %x %v", got, err)
	}
	back := ipv4Packet([4]byte{10, 66, 0, 2}, [4]byte{10, 66, 0, 1}, []byte{2})
	if err := s2.Send(protocol.TypeData, created.PeerID, back); err != nil {
		t.Fatal(err)
	}
	if _, got, err := s1.Recv(rctx); err != nil || string(got) != string(back) {
		t.Fatalf("return %x %v", got, err)
	}
}

func dialFrom(t *testing.T, local, relayAddr string, roomID uint64, peerID uint32, token [16]byte) *Session {
	t.Helper()
	sess, err := Dial(SessionConfig{
		Relay:  relayAddr,
		Local:  local,
		RoomID: roomID,
		PeerID: peerID,
		Token:  token,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func assertUDPEndpoint(t *testing.T, rooms *room.Store, roomID uint64, peerID uint32, udp *net.UDPAddr, httpIP string) {
	t.Helper()
	got, ok := rooms.Get(roomID, peerID)
	if !ok || !got.Online() {
		t.Fatal("missing endpoint")
	}
	if got.Addr.Addr().String() != udp.IP.String() || got.Addr.Port() != uint16(udp.Port) {
		t.Fatalf("endpoint %s want %s", got.Addr, udp)
	}
	if got.Addr.Addr().String() == httpIP {
		t.Fatalf("endpoint used the control address %s", httpIP)
	}
}

func ipv4Packet(src, dst [4]byte, payload []byte) []byte {
	b := make([]byte, 20+len(payload))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	b[8] = 64
	b[9] = 1
	copy(b[12:16], src[:])
	copy(b[16:20], dst[:])
	copy(b[20:], payload)
	return b
}

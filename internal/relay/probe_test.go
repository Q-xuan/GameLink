package relay

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Q-xuan/GameLink/internal/room"
	"github.com/Q-xuan/GameLink/protocol"
)

func TestHandshakeDeadlineFollowsContext(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if got := handshakeDeadline(now, context.Background()); !got.Equal(now.Add(3 * time.Second)) {
		t.Fatalf("default %s", got)
	}
	short, cancel := context.WithDeadline(context.Background(), now.Add(time.Second))
	defer cancel()
	if got := handshakeDeadline(now, short); !got.Equal(now.Add(time.Second)) {
		t.Fatalf("short %s", got)
	}
	long, cancelLong := context.WithDeadline(context.Background(), now.Add(15*time.Second))
	defer cancelLong()
	if got := handshakeDeadline(now, long); !got.Equal(now.Add(15 * time.Second)) {
		t.Fatalf("long %s", got)
	}
}

func TestPingPongRTT(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rooms := room.NewStore()
	srv := New(Options{Rooms: rooms})
	uc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	go func() { _ = srv.Serve(ctx, uc) }()

	info, host, err := rooms.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Dial(SessionConfig{
		Relay:  uc.LocalAddr().String(),
		RoomID: info.ID,
		PeerID: host.ID,
		Token:  info.Token,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	hctx, hcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer hcancel()
	if err := sess.Handshake(hctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = sess.Keepalive(ctx) }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		rctx, rcancel := context.WithTimeout(context.Background(), time.Second)
		h, _, err := sess.Recv(rctx)
		rcancel()
		if err != nil {
			continue
		}
		if h.Type != protocol.TypePong {
			continue
		}
		p := sess.Probe()
		if !p.HasRTT || p.RTT < 0 || p.RTT > 4*time.Second || p.LastPong.IsZero() || p.LastPing.IsZero() {
			t.Fatalf("%+v", p)
		}
		return
	}
	t.Fatal("no pong")
}

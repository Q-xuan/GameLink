//go:build !windows

package tun

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/internal/room"
)

func TestForwardBothWays(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rooms := room.NewStore()
	srv := relay.New(relay.Options{Rooms: rooms})
	uc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx, uc) }()

	info, host, err := rooms.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, peer2, err := rooms.Join(info.Code, info.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s1 := dialSession(t, uc.LocalAddr().String(), info.ID, host.ID, info.Token)
	s2 := dialSession(t, uc.LocalAddr().String(), info.ID, peer2.ID, info.Token)
	hctx, hcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer hcancel()
	if err := s1.Handshake(hctx); err != nil {
		t.Fatal(err)
	}
	if err := s2.Handshake(hctx); err != nil {
		t.Fatal(err)
	}

	hostDev := NewFake()
	peerDev := NewFake()
	errCh := make(chan error, 2)
	go func() { errCh <- Forward(ctx, hostDev, s1, host.VirtualIP) }()
	go func() { errCh <- Forward(ctx, peerDev, s2, peer2.VirtualIP) }()

	hostToPeer := [][]byte{
		ipv4(1, [4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{8, 0}),
		ipv4(6, [4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{0x09, 0x98, 0x00, 0x50}),
		ipv4(17, [4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{0x09, 0x99, 0x09, 0x98}),
	}
	for _, pkt := range hostToPeer {
		if err := hostDev.Inject(pkt); err != nil {
			t.Fatal(err)
		}
		got := mustCollect(t, peerDev)
		if string(got) != string(pkt) {
			t.Fatalf("peer got %x want %x", got, pkt)
		}
	}
	back := ipv4(1, [4]byte{10, 66, 0, 2}, [4]byte{10, 66, 0, 1}, []byte{0, 0})
	if err := peerDev.Inject(back); err != nil {
		t.Fatal(err)
	}
	if got := mustCollect(t, hostDev); string(got) != string(back) {
		t.Fatalf("host got %x", got)
	}

	drops := [][]byte{
		{0x60, 0, 0, 0, 0, 8, 58, 64},
		ipv4(1, [4]byte{10, 66, 0, 1}, [4]byte{255, 255, 255, 255}, nil),
		ipv4(1, [4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 255}, nil),
		ipv4(17, [4]byte{10, 66, 0, 1}, [4]byte{224, 0, 0, 1}, []byte{1}),
		ipv4(17, [4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 9}, []byte{1}),
		ipv4(1, [4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 1}, []byte{1}),
	}
	for _, pkt := range drops {
		if err := hostDev.Inject(pkt); err != nil {
			t.Fatal(err)
		}
	}
	keep := ipv4(17, [4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte("ok"))
	if err := hostDev.Inject(keep); err != nil {
		t.Fatal(err)
	}
	if got := mustCollect(t, peerDev); string(got) != string(keep) {
		t.Fatalf("after drops got %x", got)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forward did not stop")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second forward did not stop")
	}
}

func dialSession(t *testing.T, relayAddr string, roomID uint64, peerID uint32, token [16]byte) *relay.Session {
	t.Helper()
	sess, err := relay.Dial(relay.SessionConfig{
		Relay:  relayAddr,
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

func mustCollect(t *testing.T, dev *Fake) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pkt, err := dev.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func ipv4(proto byte, src, dst [4]byte, payload []byte) []byte {
	b := make([]byte, 20+len(payload))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	b[8] = 64
	b[9] = proto
	copy(b[12:16], src[:])
	copy(b[16:20], dst[:])
	copy(b[20:], payload)
	return b
}

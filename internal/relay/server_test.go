package relay

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Q-xuan/GameLink/internal/peer"
	"github.com/Q-xuan/GameLink/internal/room"
	"github.com/Q-xuan/GameLink/protocol"
)

type recordingObserver struct {
	mu   sync.Mutex
	up   []uint32
	down []uint32
}

func (r *recordingObserver) PeerUp(p peer.Peer) {
	r.mu.Lock()
	r.up = append(r.up, p.ID)
	r.mu.Unlock()
}

func (r *recordingObserver) PeerDown(p peer.Peer) {
	r.mu.Lock()
	r.down = append(r.down, p.ID)
	r.mu.Unlock()
}

func (r *recordingObserver) downs() []uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint32(nil), r.down...)
}

func startRelay(t *testing.T, insecure bool, idle, sweep time.Duration) (*room.Store, *net.UDPConn, *recordingObserver, context.CancelFunc) {
	t.Helper()
	rooms := room.NewStore()
	obs := &recordingObserver{}
	srv := New(Options{
		Rooms:       rooms,
		Observer:    obs,
		Insecure:    insecure,
		IdleTimeout: idle,
		SweepEvery:  sweep,
	})
	uc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx, uc) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("relay did not stop")
		}
	})
	return rooms, uc, obs, cancel
}

func udpClient(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func sendPkt(t *testing.T, c *net.UDPConn, to net.Addr, token [16]byte, h protocol.Header, payload []byte, mac bool) {
	t.Helper()
	h.Version = protocol.Version
	var buf []byte
	var err error
	if mac {
		buf, err = Seal(token, h, payload)
	} else {
		buf, err = protocol.Marshal(h, payload)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteTo(buf, to); err != nil {
		t.Fatal(err)
	}
}

func readWithin(t *testing.T, c *net.UDPConn, d time.Duration) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 65535)
	n, _, err := c.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buf[:n]...)
}

func readNone(t *testing.T, c *net.UDPConn, d time.Duration) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 2048)
	n, _, err := c.ReadFrom(buf)
	if err == nil {
		t.Fatalf("unexpected %d bytes", n)
	}
}

func handshake(t *testing.T, c *net.UDPConn, relay net.Addr, roomID uint64, peerID uint32, token [16]byte, mac bool) {
	t.Helper()
	sendPkt(t, c, relay, token, protocol.Header{
		Type:    protocol.TypeHandshake,
		RoomID:  roomID,
		SrcPeer: peerID,
	}, token[:], mac)
	buf := readWithin(t, c, time.Second)
	h, _, err := Authenticate(token, buf, mac)
	if err != nil {
		t.Fatal(err)
	}
	if h.Type != protocol.TypeHandshake || h.DstPeer != peerID {
		t.Fatalf("handshake reply %+v", h)
	}
}

func ipv4Raw(src, dst [4]byte, tail []byte) []byte {
	b := make([]byte, 20+len(tail))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	b[8] = 64
	b[9] = 17
	copy(b[12:16], src[:])
	copy(b[16:20], dst[:])
	copy(b[20:], tail)
	return b
}

func TestForwardBothWaysMACAndPorts(t *testing.T) {
	rooms, relay, _, _ := startRelay(t, false, 0, 0)
	info, _, err := rooms.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := rooms.Join(info.Code, info.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c1 := udpClient(t)
	c2 := udpClient(t)
	handshake(t, c1, relay.LocalAddr(), info.ID, 1, info.Token, true)
	handshake(t, c2, relay.LocalAddr(), info.ID, p2.ID, info.Token, true)

	// Tail bytes look like two different game ports. The relay must not branch on them.
	p2456 := ipv4Raw([4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{0x09, 0x98, 1})
	p2457 := ipv4Raw([4]byte{10, 66, 0, 2}, [4]byte{10, 66, 0, 1}, []byte{0x09, 0x99, 2})
	sendData := func(c *net.UDPConn, src, dst uint32, seq uint32, payload []byte) {
		sendPkt(t, c, relay.LocalAddr(), info.Token, protocol.Header{
			Type: protocol.TypeData, RoomID: info.ID, SrcPeer: src, DstPeer: dst, Sequence: seq,
		}, payload, true)
	}
	sendData(c1, 1, p2.ID, 5, p2456)
	got := readWithin(t, c2, time.Second)
	_, payload, err := Authenticate(info.Token, got, true)
	if err != nil || string(payload) != string(p2456) {
		t.Fatalf("c2 got %x err %v", payload, err)
	}
	sendData(c2, p2.ID, 1, 5, p2457)
	got = readWithin(t, c1, time.Second)
	_, payload, err = Authenticate(info.Token, got, true)
	if err != nil || string(payload) != string(p2457) {
		t.Fatalf("c1 got %x err %v", payload, err)
	}
	// Duplicate sequence is still forwarded.
	sendData(c1, 1, p2.ID, 5, p2456)
	if _, payload, err = Authenticate(info.Token, readWithin(t, c2, time.Second), true); err != nil || string(payload) != string(p2456) {
		t.Fatal(err)
	}

	bad := append([]byte(nil), mustSeal(t, info.Token, protocol.Header{
		Type: protocol.TypeData, RoomID: info.ID, SrcPeer: 1, DstPeer: p2.ID, Sequence: 9,
	}, p2456)...)
	bad[len(bad)-1] ^= 0xff
	if _, err := c1.WriteTo(bad, relay.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	readNone(t, c2, 200*time.Millisecond)

	plain, err := protocol.Marshal(protocol.Header{
		Version: protocol.Version, Type: protocol.TypeData, RoomID: info.ID, SrcPeer: 1, DstPeer: p2.ID,
	}, p2456)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c1.WriteTo(plain, relay.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	readNone(t, c2, 200*time.Millisecond)
}

func TestDropNonUnicastIPv4(t *testing.T) {
	rooms, relay, _, _ := startRelay(t, false, 0, 0)
	info, _, err := rooms.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := rooms.Join(info.Code, info.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c1 := udpClient(t)
	c2 := udpClient(t)
	handshake(t, c1, relay.LocalAddr(), info.ID, 1, info.Token, true)
	handshake(t, c2, relay.LocalAddr(), info.ID, p2.ID, info.Token, true)
	drops := [][]byte{
		{0x60, 0, 0, 20, 0, 0, 0, 0, 64, 17, 0, 0, 10, 66, 0, 1, 10, 66, 0, 2},
		ipv4Raw([4]byte{10, 66, 0, 1}, [4]byte{255, 255, 255, 255}, nil),
		ipv4Raw([4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 255}, nil),
		ipv4Raw([4]byte{10, 66, 0, 1}, [4]byte{224, 0, 0, 1}, nil),
		ipv4Raw([4]byte{10, 66, 0, 9}, [4]byte{10, 66, 0, 2}, nil),
		{0x45},
	}
	for _, p := range drops {
		sendPkt(t, c1, relay.LocalAddr(), info.Token, protocol.Header{
			Type: protocol.TypeData, RoomID: info.ID, SrcPeer: 1, DstPeer: p2.ID, Sequence: 1,
		}, p, true)
	}
	readNone(t, c2, 200*time.Millisecond)
	good := ipv4Raw([4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{7})
	sendPkt(t, c1, relay.LocalAddr(), info.Token, protocol.Header{
		Type: protocol.TypeData, RoomID: info.ID, SrcPeer: 1, DstPeer: p2.ID, Sequence: 2,
	}, good, true)
	_, payload, err := Authenticate(info.Token, readWithin(t, c2, time.Second), true)
	if err != nil || string(payload) != string(good) {
		t.Fatalf("good path %x %v", payload, err)
	}
}

func TestInsecureAcceptsMissingMACAndRejectsBadMAC(t *testing.T) {
	rooms, relay, _, _ := startRelay(t, true, 0, 0)
	info, _, err := rooms.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := rooms.Join(info.Code, info.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c1 := udpClient(t)
	c2 := udpClient(t)
	handshake(t, c1, relay.LocalAddr(), info.ID, 1, info.Token, false)
	handshake(t, c2, relay.LocalAddr(), info.ID, p2.ID, info.Token, false)
	payload := ipv4Raw([4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{1})
	sendPkt(t, c1, relay.LocalAddr(), info.Token, protocol.Header{
		Type: protocol.TypeData, RoomID: info.ID, SrcPeer: 1, DstPeer: p2.ID,
	}, payload, false)
	if _, got, err := Authenticate(info.Token, readWithin(t, c2, time.Second), false); err != nil || string(got) != string(payload) {
		t.Fatal(err)
	}
	sealed := mustSeal(t, info.Token, protocol.Header{
		Type: protocol.TypeData, RoomID: info.ID, SrcPeer: 1, DstPeer: p2.ID, Sequence: 3,
	}, payload)
	sealed[len(sealed)-1] ^= 0x5a
	if _, err := c1.WriteTo(sealed, relay.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	readNone(t, c2, 200*time.Millisecond)
}

func TestPeerAddressRebind(t *testing.T) {
	rooms, relay, _, _ := startRelay(t, false, 0, 0)
	info, host, err := rooms.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := rooms.Join(info.Code, info.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c1 := udpClient(t)
	c1b := udpClient(t)
	c2 := udpClient(t)
	handshake(t, c1, relay.LocalAddr(), info.ID, host.ID, info.Token, true)
	handshake(t, c2, relay.LocalAddr(), info.ID, p2.ID, info.Token, true)
	sendPkt(t, c1b, relay.LocalAddr(), info.Token, protocol.Header{
		Type: protocol.TypePing, RoomID: info.ID, SrcPeer: host.ID,
	}, nil, true)
	pong := readWithin(t, c1b, time.Second)
	if h, _, err := Authenticate(info.Token, pong, true); err != nil || h.Type != protocol.TypePong {
		t.Fatal(err, h.Type)
	}
	got, ok := rooms.Get(info.ID, host.ID)
	if !ok || got.VirtualIP.String() != "10.66.0.1" || got.Addr.Port() != uint16(c1b.LocalAddr().(*net.UDPAddr).Port) {
		t.Fatalf("rebind %+v c1b %s", got, c1b.LocalAddr())
	}
	payload := ipv4Raw([4]byte{10, 66, 0, 2}, [4]byte{10, 66, 0, 1}, []byte{9})
	sendPkt(t, c2, relay.LocalAddr(), info.Token, protocol.Header{
		Type: protocol.TypeData, RoomID: info.ID, SrcPeer: p2.ID, DstPeer: host.ID,
	}, payload, true)
	if _, gotPayload, err := Authenticate(info.Token, readWithin(t, c1b, time.Second), true); err != nil || string(gotPayload) != string(payload) {
		t.Fatal(err)
	}
	readNone(t, c1, 150*time.Millisecond)
}

func TestIdleTimeout(t *testing.T) {
	rooms, relay, obs, _ := startRelay(t, false, 40*time.Millisecond, 10*time.Millisecond)
	info, host, err := rooms.Create(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := rooms.Join(info.Code, info.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c1 := udpClient(t)
	c2 := udpClient(t)
	handshake(t, c1, relay.LocalAddr(), info.ID, host.ID, info.Token, true)
	handshake(t, c2, relay.LocalAddr(), info.ID, p2.ID, info.Token, true)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := rooms.Get(info.ID, host.ID); !ok && len(obs.downs()) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := rooms.Get(info.ID, host.ID); ok {
		t.Fatal("peer still present")
	}
	downs := obs.downs()
	if len(downs) < 2 {
		t.Fatalf("downs %v", downs)
	}
	payload := ipv4Raw([4]byte{10, 66, 0, 1}, [4]byte{10, 66, 0, 2}, []byte{1})
	sendPkt(t, c1, relay.LocalAddr(), info.Token, protocol.Header{
		Type: protocol.TypeData, RoomID: info.ID, SrcPeer: 1, DstPeer: 2,
	}, payload, true)
	readNone(t, c2, 150*time.Millisecond)
}

func mustSeal(t *testing.T, token [16]byte, h protocol.Header, payload []byte) []byte {
	t.Helper()
	h.Version = protocol.Version
	buf, err := Seal(token, h, payload)
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

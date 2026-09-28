package main

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestHostClientCounters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	game, _, err := StartHost(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	ua, err := net.ResolveUDPAddr("udp4", game)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := RunClient(ctx, ClientConfig{
		Host:     "127.0.0.1",
		Port:     ua.Port,
		PPS:      50,
		Size:     64,
		Duration: 200 * time.Millisecond,
		Session:  7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sent == 0 || rep.Recv != rep.Sent || rep.Loss != 0 {
		t.Fatalf("%s", rep)
	}
	if rep.Throughput <= 0 {
		t.Fatal(rep.Throughput)
	}
}

func TestHostAcksGameAndQueryPorts(t *testing.T) {
	port := freeUDPPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	game, query, err := StartHost(ctx, "127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	if game != "127.0.0.1:"+itoa(port) || query != "127.0.0.1:"+itoa(port+1) {
		t.Fatalf("game %s query %s", game, query)
	}
	for _, addr := range []string{game, query} {
		if err := roundTrip(addr); err != nil {
			t.Fatal(addr, err)
		}
	}
}

func TestRelayPairCounters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b, err := RunRelayPair(ctx, RelayPairConfig{Packets: 20, Size: 128})
	if err != nil {
		t.Fatal(err)
	}
	if a.Sent != 20 || a.Recv != 20 || a.Loss != 0 {
		t.Fatalf("peer1 %s", a)
	}
	if b.Sent != 20 || b.Recv != 20 || b.Loss != 0 {
		t.Fatalf("peer2 %s", b)
	}
}

func roundTrip(addr string) error {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return err
	}
	c, err := net.DialUDP("udp4", nil, ua)
	if err != nil {
		return err
	}
	defer c.Close()
	raw, err := Encode(Packet{SessionID: 1, Sequence: 1, Timestamp: time.Now().UnixNano(), Kind: KindData}, MinSize)
	if err != nil {
		return err
	}
	if _, err := c.Write(raw); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, MaxSize)
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	pkt, err := Decode(buf[:n])
	if err != nil {
		return err
	}
	if pkt.Kind != KindAck || pkt.AckSeq != 1 {
		return errAck
	}
	return nil
}

var errAck = errorString("missing ack")

type errorString string

func (e errorString) Error() string { return string(e) }

func freeUDPPair(t *testing.T) int {
	t.Helper()
	for i := 0; i < 8; i++ {
		a, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		port := a.LocalAddr().(*net.UDPAddr).Port
		b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port + 1})
		_ = a.Close()
		if err != nil {
			continue
		}
		_ = b.Close()
		return port
	}
	t.Fatal("no free udp port pair")
	return 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

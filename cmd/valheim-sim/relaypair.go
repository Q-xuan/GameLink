package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/Q-xuan/GameLink/internal/control"
	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/internal/room"
	"github.com/Q-xuan/GameLink/protocol"
)

// RelayPairConfig runs two simulator peers through an in-process relay.
// There is no TUN device. Traffic stays on the loopback interface.
type RelayPairConfig struct {
	Packets  int
	Size     int
	Insecure bool
}

// RunRelayPair creates a room, joins a second peer, and exchanges simulator
// packets in both directions over the UDP relay.
func RunRelayPair(ctx context.Context, cfg RelayPairConfig) (Report, Report, error) {
	if cfg.Packets <= 0 {
		cfg.Packets = 20
	}
	if cfg.Size == 0 {
		cfg.Size = 128
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	rooms := room.NewStore()
	hub := control.NewHub(rooms)
	uc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return Report{}, Report{}, err
	}
	defer uc.Close()
	if err := ensureLoopback(uc.LocalAddr().String()); err != nil {
		return Report{}, Report{}, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Report{}, Report{}, err
	}
	defer ln.Close()

	relayAddr := uc.LocalAddr().String()
	controlURL := "http://" + ln.Addr().String()
	fwd := relay.New(relay.Options{
		Rooms:       rooms,
		Observer:    hub,
		Insecure:    cfg.Insecure,
		IdleTimeout: relay.IdleTimeout,
	})
	ctrl := control.New(control.Options{
		Rooms:            rooms,
		Hub:              hub,
		PublicRelay:      relayAddr,
		PublicControlURL: controlURL,
	})
	go func() { _ = fwd.Serve(ctx, uc) }()
	go func() { _ = ctrl.Serve(ctx, ln) }()
	if err := waitHealth(ctx, controlURL); err != nil {
		return Report{}, Report{}, err
	}

	created, err := control.CreateRoom(ctx, controlURL)
	if err != nil {
		return Report{}, Report{}, err
	}
	joined, err := control.JoinRoom(ctx, controlURL, created.Code, created.Token)
	if err != nil {
		return Report{}, Report{}, err
	}
	if err := ensureLoopback(created.Relay); err != nil {
		return Report{}, Report{}, err
	}
	if err := ensureLoopback(joined.Relay); err != nil {
		return Report{}, Report{}, err
	}
	token, err := room.ParseToken(created.Token)
	if err != nil {
		return Report{}, Report{}, err
	}
	roomID, err := strconv.ParseUint(created.RoomID, 10, 64)
	if err != nil {
		return Report{}, Report{}, err
	}
	left, err := relay.Dial(relay.SessionConfig{
		Relay: created.Relay, RoomID: roomID, PeerID: created.PeerID, Token: token, Insecure: cfg.Insecure,
	})
	if err != nil {
		return Report{}, Report{}, err
	}
	defer left.Close()
	right, err := relay.Dial(relay.SessionConfig{
		Relay: joined.Relay, RoomID: roomID, PeerID: joined.PeerID, Token: token, Insecure: cfg.Insecure,
	})
	if err != nil {
		return Report{}, Report{}, err
	}
	defer right.Close()
	if err := left.Handshake(ctx); err != nil {
		return Report{}, Report{}, err
	}
	if err := right.Handshake(ctx); err != nil {
		return Report{}, Report{}, err
	}

	vipA := netip.MustParseAddr(created.VIP)
	vipB := netip.MustParseAddr(joined.VIP)
	var repA, repB Report
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		repA, errA = exchange(ctx, left, vipA, vipB, joined.PeerID, 1, cfg)
	}()
	go func() {
		defer wg.Done()
		repB, errB = exchange(ctx, right, vipB, vipA, created.PeerID, 2, cfg)
	}()
	wg.Wait()
	if errA != nil {
		return repA, repB, errA
	}
	if errB != nil {
		return repA, repB, errB
	}
	return repA, repB, nil
}

func exchange(ctx context.Context, sess *relay.Session, src, dst netip.Addr, dstPeer, sessionID uint32, cfg RelayPairConfig) (Report, error) {
	readCtx, readCancel := context.WithCancel(ctx)
	defer readCancel()
	var col Collector
	col.Start()
	errCh := make(chan error, 1)
	go func() {
		errCh <- readRelaySim(readCtx, sess, src, sessionID, &col)
	}()
	for seq := uint32(1); seq <= uint32(cfg.Packets); seq++ {
		raw, err := Encode(Packet{
			SessionID: sessionID,
			Sequence:  seq,
			Timestamp: time.Now().UnixNano(),
			Kind:      KindData,
		}, cfg.Size)
		if err != nil {
			readCancel()
			<-errCh
			return col.Finish(), err
		}
		wrapped, err := WrapIPv4UDP(src, dst, 2456, 2456, raw)
		if err != nil {
			readCancel()
			<-errCh
			return col.Finish(), err
		}
		if err := sess.Send(protocol.TypeData, dstPeer, wrapped); err != nil {
			readCancel()
			<-errCh
			return col.Finish(), err
		}
		col.AddSend(len(raw))
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && col.RecvCount() < uint64(cfg.Packets) {
		select {
		case <-ctx.Done():
			readCancel()
			<-errCh
			return col.Finish(), ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	readCancel()
	if err := <-errCh; err != nil {
		return col.Finish(), err
	}
	rep := col.Finish()
	if rep.Recv != uint64(cfg.Packets) {
		return rep, fmt.Errorf("recv %d want %d", rep.Recv, cfg.Packets)
	}
	return rep, nil
}

func readRelaySim(ctx context.Context, sess *relay.Session, self netip.Addr, sessionID uint32, col *Collector) error {
	for {
		h, payload, err := sess.Recv(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if h.Type != protocol.TypeData {
			continue
		}
		src, _, _, _, inner, err := UnwrapIPv4UDP(payload)
		if err != nil {
			continue
		}
		pkt, err := Decode(inner)
		if err != nil {
			continue
		}
		if pkt.Kind == KindData {
			raw, err := Encode(Packet{
				SessionID: pkt.SessionID,
				Sequence:  pkt.Sequence,
				Timestamp: pkt.Timestamp,
				AckSeq:    pkt.Sequence,
				Kind:      KindAck,
			}, MinSize)
			if err != nil {
				continue
			}
			wrapped, err := WrapIPv4UDP(self, src, 2456, 2456, raw)
			if err != nil {
				continue
			}
			if err := sess.Send(protocol.TypeData, h.SrcPeer, wrapped); err != nil {
				return err
			}
			continue
		}
		if pkt.Kind == KindAck && pkt.SessionID == sessionID {
			rtt := time.Since(time.Unix(0, pkt.Timestamp))
			if rtt < 0 {
				rtt = 0
			}
			col.AddRecv(rtt)
		}
	}
}

func waitHealth(ctx context.Context, base string) error {
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			last = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && string(body) == `{"status":"ok"}` {
			return nil
		}
		last = fmt.Errorf("healthz %d %s", resp.StatusCode, body)
		time.Sleep(10 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("healthz timeout")
	}
	return last
}

func ensureLoopback(hostport string) error {
	ua, err := net.ResolveUDPAddr("udp4", hostport)
	if err != nil {
		return err
	}
	if ua.IP == nil || !ua.IP.IsLoopback() {
		return fmt.Errorf("refusing non-loopback relay %s", hostport)
	}
	return nil
}

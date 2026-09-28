package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"math/big"
	"net"
	"strconv"
	"sync"
	"time"
)

// ClientConfig is the direct UDP sender used by `valheim-sim client`.
type ClientConfig struct {
	Host     string
	Port     int
	PPS      int
	Size     int
	Duration time.Duration
	Session  uint32
}

// RunClient sends simulator datagrams and waits briefly for ACKs.
func RunClient(ctx context.Context, cfg ClientConfig) (Report, error) {
	if cfg.PPS <= 0 {
		cfg.PPS = 20
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 10 * time.Second
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Session == 0 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Report{}, err
		}
		cfg.Session = binary.BigEndian.Uint32(b[:])
		if cfg.Session == 0 {
			cfg.Session = 1
		}
	}
	raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return Report{}, err
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return Report{}, err
	}
	defer conn.Close()

	var col Collector
	col.Start()
	var mu sync.Mutex
	pending := map[uint32]struct{}{}
	recvCtx, recvCancel := context.WithCancel(ctx)
	defer recvCancel()
	go func() {
		buf := make([]byte, MaxSize+64)
		for recvCtx.Err() == nil {
			_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := conn.Read(buf)
			if err != nil {
				continue
			}
			pkt, err := Decode(buf[:n])
			if err != nil || pkt.Kind != KindAck || pkt.SessionID != cfg.Session {
				continue
			}
			mu.Lock()
			if _, ok := pending[pkt.AckSeq]; ok {
				delete(pending, pkt.AckSeq)
				rtt := time.Since(time.Unix(0, pkt.Timestamp))
				if rtt < 0 {
					rtt = 0
				}
				col.AddRecv(rtt)
			}
			mu.Unlock()
		}
	}()

	interval := time.Second / time.Duration(cfg.PPS)
	if interval <= 0 {
		interval = time.Millisecond
	}
	var seq uint32
	sendOne := func() error {
		seq++
		size, err := pickSize(cfg.Size)
		if err != nil {
			return err
		}
		raw, err := Encode(Packet{
			SessionID: cfg.Session,
			Sequence:  seq,
			Timestamp: time.Now().UnixNano(),
			Kind:      KindData,
		}, size)
		if err != nil {
			return err
		}
		mu.Lock()
		pending[seq] = struct{}{}
		mu.Unlock()
		if _, err := conn.Write(raw); err != nil {
			mu.Lock()
			delete(pending, seq)
			mu.Unlock()
			return err
		}
		col.AddSend(len(raw))
		return nil
	}
	if err := sendOne(); err != nil {
		return Report{}, err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.Now().Add(cfg.Duration)
	sending := true
	for sending {
		select {
		case <-ctx.Done():
			return col.Finish(), ctx.Err()
		case now := <-ticker.C:
			if now.After(deadline) {
				sending = false
				continue
			}
			if err := sendOne(); err != nil {
				return col.Finish(), err
			}
		}
	}
	waitUntil := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(waitUntil) {
		mu.Lock()
		left := len(pending)
		mu.Unlock()
		if left == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	recvCancel()
	return col.Finish(), nil
}

func pickSize(fixed int) (int, error) {
	if fixed > 0 {
		if fixed < MinSize {
			fixed = MinSize
		}
		if fixed > MaxSize {
			fixed = MaxSize
		}
		return fixed, nil
	}
	span := MaxSize - MinSize + 1
	n, err := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if err != nil {
		return 0, err
	}
	return MinSize + int(n.Int64()), nil
}

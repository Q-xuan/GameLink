package tun

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"

	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/protocol"
)

// Forward copies unicast IPv4 between dev and the relay session until ctx ends.
// Outgoing packets are sealed with the session's HMAC. Inbound packets are
// verified by Session.Recv before they are written to dev.
func Forward(parent context.Context, dev Device, sess *relay.Session, local netip.Addr) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = dev.Close()
	}()

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- pumpDevice(ctx, dev, sess, local)
	}()
	go func() {
		defer wg.Done()
		errCh <- pumpRelay(ctx, dev, sess)
	}()

	select {
	case <-parent.Done():
		cancel()
		wg.Wait()
		return nil
	case err := <-errCh:
		cancel()
		wg.Wait()
		if parent.Err() != nil {
			return nil
		}
		return err
	}
}

func pumpDevice(ctx context.Context, dev Device, sess *relay.Session, local netip.Addr) error {
	for {
		pkt, err := dev.Read()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		dst, ok := destPeer(pkt, local)
		if !ok {
			continue
		}
		if err := sess.Send(protocol.TypeData, dst, pkt); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

func pumpRelay(ctx context.Context, dev Device, sess *relay.Session) error {
	for {
		h, payload, err := sess.Recv(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if h.Type != protocol.TypeData || !deliverable(payload) {
			continue
		}
		if _, err := dev.Write(payload); err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
	}
}

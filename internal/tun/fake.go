//go:build !windows

package tun

import (
	"context"
	"net"
	"sync"
)

// Fake is an in-memory adapter. Tests inject packets the forwarder should
// read, and collect packets the forwarder writes.
type Fake struct {
	mu     sync.Mutex
	closed bool
	in     chan []byte
	out    chan []byte
	done   chan struct{}
}

func NewFake() *Fake {
	return &Fake{
		in:   make(chan []byte, 32),
		out:  make(chan []byte, 32),
		done: make(chan struct{}),
	}
}

func (f *Fake) Read() ([]byte, error) {
	select {
	case <-f.done:
		return nil, net.ErrClosed
	case pkt := <-f.in:
		return pkt, nil
	}
}

func (f *Fake) Write(pkt []byte) (int, error) {
	cp := append([]byte(nil), pkt...)
	select {
	case <-f.done:
		return 0, net.ErrClosed
	case f.out <- cp:
		return len(pkt), nil
	}
}

func (f *Fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.done)
	}
	return nil
}

func (f *Fake) MTU() int { return MTU }

// Inject queues one packet as if the operating system sent it.
func (f *Fake) Inject(pkt []byte) error {
	cp := append([]byte(nil), pkt...)
	select {
	case <-f.done:
		return net.ErrClosed
	case f.in <- cp:
		return nil
	}
}

// Collect waits for one packet written toward the operating system.
func (f *Fake) Collect(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		return nil, net.ErrClosed
	case pkt := <-f.out:
		return pkt, nil
	}
}

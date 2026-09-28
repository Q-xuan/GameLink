//go:build !windows

package main

import (
	"context"
	"fmt"

	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/protocol"
)

func runTunnel(ctx context.Context, sess *relay.Session, vip string) error {
	_ = vip
	for {
		h, payload, err := sess.Recv(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("接收失败: %v", err)
		}
		if h.Type == protocol.TypeData {
			fmt.Printf("data src_peer=%d bytes=%d seq=%d\n", h.SrcPeer, len(payload), h.Sequence)
		}
	}
}

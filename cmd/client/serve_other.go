//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/protocol"
)

func serve(ctx context.Context, sess *relay.Session, vip string) int {
	_ = vip
	for {
		h, payload, err := sess.Recv(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "接收失败: %v\n", err)
			return 1
		}
		if h.Type == protocol.TypeData {
			fmt.Printf("data src_peer=%d bytes=%d seq=%d\n", h.SrcPeer, len(payload), h.Sequence)
		}
	}
}

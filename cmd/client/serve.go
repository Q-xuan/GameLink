package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Q-xuan/GameLink/internal/relay"
)

func serve(ctx context.Context, sess *relay.Session, vip string) int {
	if err := runTunnel(ctx, sess, vip); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	return 0
}

//go:build windows

package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"

	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/internal/route"
	"github.com/Q-xuan/GameLink/internal/tun"
)

func serve(ctx context.Context, sess *relay.Session, vip string) int {
	addr, err := netip.ParseAddr(vip)
	if err != nil {
		fmt.Fprintf(os.Stderr, "虚拟地址无效: %v\n", err)
		return 1
	}
	plan, err := route.ForVIP(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "路由计划无效: %v\n", err)
		return 1
	}
	dev, luid, closeAdapter, err := tun.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	defer closeAdapter()
	cleanup, err := route.Install(luid, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置地址或路由失败: %v\n", err)
		return 1
	}
	defer cleanup()
	fmt.Fprintf(os.Stderr, "网卡 %s 已启动，地址 %s，MTU %d\n", plan.Adapter, plan.Address, plan.MTU)
	for _, rt := range plan.Routes {
		fmt.Fprintf(os.Stderr, "路由 %s on-link 已添加到该网卡\n", rt.Prefix)
	}
	if err := tun.Forward(ctx, dev, sess, addr); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "转发失败: %v\n", err)
		return 1
	}
	return 0
}

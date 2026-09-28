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

func runTunnel(ctx context.Context, sess *relay.Session, vip string) error {
	addr, err := netip.ParseAddr(vip)
	if err != nil {
		return fmt.Errorf("虚拟地址无效: %v", err)
	}
	plan, err := route.ForVIP(addr)
	if err != nil {
		return fmt.Errorf("路由计划无效: %v", err)
	}
	dev, luid, closeAdapter, err := tun.Open()
	if err != nil {
		return err
	}
	defer closeAdapter()
	cleanup, err := route.Install(luid, addr)
	if err != nil {
		return fmt.Errorf("配置地址或路由失败: %v", err)
	}
	defer cleanup()
	fmt.Fprintf(os.Stderr, "网卡 %s 已启动，地址 %s，MTU %d\n", plan.Adapter, plan.Address, plan.MTU)
	for _, rt := range plan.Routes {
		fmt.Fprintf(os.Stderr, "路由 %s on-link 已添加到该网卡\n", rt.Prefix)
	}
	if err := tun.Forward(ctx, dev, sess, addr); err != nil && ctx.Err() == nil {
		return fmt.Errorf("转发失败: %v", err)
	}
	return nil
}

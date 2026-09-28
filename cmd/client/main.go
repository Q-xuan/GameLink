package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Q-xuan/GameLink/internal/config"
	"github.com/Q-xuan/GameLink/internal/control"
	"github.com/Q-xuan/GameLink/internal/invite"
	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/internal/room"
	"github.com/Q-xuan/GameLink/internal/tun"
)

func main() {
	if len(os.Args) < 2 {
		if guiAvailable {
			os.Exit(runGUI(""))
		}
		usage()
		os.Exit(2)
	}
	if invite.IsURL(os.Args[1]) {
		if guiAvailable {
			os.Exit(runGUI(os.Args[1]))
		}
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "host":
		code = cmdHost(os.Args[2:])
	case "join":
		code = cmdJoin(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		code = 0
	default:
		usage()
		code = 2
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintf(os.Stderr, `gamelink 控制一个房间，并保持到 UDP 中继的会话。

用法:
  gamelink host [--control URL] [--relay HOST:PORT] [--insecure]
  gamelink join <code> --token <hex> [--control URL] [--relay HOST:PORT] [--insecure]

一个进程只加入一个房间。默认控制面是 %s，默认中继是 %s。
Windows 上双击本程序会打开中文窗口，并在 UAC 提示里请求管理员权限。
命令行仍会创建 Wintun 网卡 GameLink。本地服务器请同时传入 --control 和 --relay，否则会使用创建房间时返回的中继地址。
`, config.DefaultPublicControlURL, config.DefaultPublicRelay)
}

func cmdHost(args []string) int {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	controlURL := fs.String("control", config.DefaultControlURL(), "控制面 URL")
	relayOverride := fs.String("relay", "", "覆盖服务器返回的 UDP 中继地址")
	insecure := fs.Bool("insecure", false, "不附加 MAC，仅用于本机测试")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := prepareClient(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if err := tun.RequireAdmin(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	created, err := control.CreateRoom(ctx, *controlURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建房间失败: %v\n", err)
		return 1
	}
	fmt.Printf("code %s\n", created.Code)
	fmt.Printf("token %s\n", created.Token)
	fmt.Printf("room_id %s\n", created.RoomID)
	fmt.Printf("peer_id %d\n", created.PeerID)
	fmt.Printf("vip %s\n", created.VIP)
	relayAddr := created.Relay
	if *relayOverride != "" {
		relayAddr = *relayOverride
	}
	fmt.Printf("relay %s\n", relayAddr)
	fmt.Printf("control %s\n", created.ControlURL)
	return runSession(ctx, *controlURL, created.Code, created.Token, created.RoomID, created.PeerID, created.VIP, relayAddr, *insecure)
}

func cmdJoin(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
		return 2
	}
	code := args[0]
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	token := fs.String("token", "", "128 位房间令牌的十六进制")
	controlURL := fs.String("control", config.DefaultControlURL(), "控制面 URL")
	relayOverride := fs.String("relay", "", "覆盖服务器返回的 UDP 中继地址")
	insecure := fs.Bool("insecure", false, "不附加 MAC，仅用于本机测试")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "join 需要 --token")
		return 2
	}
	if err := prepareClient(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if err := tun.RequireAdmin(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	joined, err := control.JoinRoom(ctx, *controlURL, code, *token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加入房间失败: %v\n", err)
		return 1
	}
	fmt.Printf("code %s\n", joined.Code)
	fmt.Printf("room_id %s\n", joined.RoomID)
	fmt.Printf("peer_id %d\n", joined.PeerID)
	fmt.Printf("vip %s\n", joined.VIP)
	relayAddr := joined.Relay
	if *relayOverride != "" {
		relayAddr = *relayOverride
	}
	fmt.Printf("relay %s\n", relayAddr)
	return runSession(ctx, *controlURL, joined.Code, *token, joined.RoomID, joined.PeerID, joined.VIP, relayAddr, *insecure)
}

func runSession(ctx context.Context, controlURL, code, tokenHex, roomText string, peerID uint32, vip, relayAddr string, insecure bool) int {
	token, err := room.ParseToken(tokenHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "token: %v\n", err)
		return 2
	}
	roomID, err := strconv.ParseUint(roomText, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "room_id: %v\n", err)
		return 2
	}
	sess, err := relay.Dial(relay.SessionConfig{
		Relay:    relayAddr,
		RoomID:   roomID,
		PeerID:   peerID,
		Token:    token,
		Insecure: insecure,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接中继失败: %v\n", err)
		return 1
	}
	defer sess.Close()
	fmt.Fprintf(os.Stderr, "正在握手 %s\n", relayAddr)
	if err := sess.Handshake(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "握手失败: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "握手完成，每 5 秒发送 Ping")
	go streamEvents(ctx, controlURL, code, tokenHex, peerID)
	go func() {
		if err := sess.Keepalive(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "ping: %v\n", err)
		}
	}()
	return serve(ctx, sess, vip)
}

func streamEvents(ctx context.Context, controlURL, code, token string, peerID uint32) {
	for ctx.Err() == nil {
		conn, err := control.DialEvents(ctx, controlURL, code, token, peerID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "事件连接失败: %v\n", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				_ = conn.Close()
				if ctx.Err() != nil {
					return
				}
				fmt.Fprintf(os.Stderr, "事件连接中断: %v\n", err)
				break
			}
			fmt.Printf("event %s\n", msg)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

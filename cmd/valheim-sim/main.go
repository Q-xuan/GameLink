package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "host":
		code = cmdHost(os.Args[2:])
	case "client":
		code = cmdClient(os.Args[2:])
	case "relay":
		code = cmdRelay(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		code = 2
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintf(os.Stderr, `valheim-sim 生成类似游戏房间的 UDP 流量。

用法:
  valheim-sim host --port 2456
  valheim-sim client --host <ip> --port 2456 [--pps 20] [--size 0] [--duration 10s]
  valheim-sim relay [--count 20] [--size 128]

host 同时监听 port 和 port+1。client 发送 SessionID、Sequence、Timestamp 和填充，
长度在 64 到 1200 之间，并统计 sent/recv、丢包、RTT、抖动和吞吐。
relay 在本机拉起中继，让两个模拟端互发，不经过 TUN。
`)
}

func cmdHost(args []string) int {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bind := fs.String("bind", "0.0.0.0", "监听地址")
	port := fs.Int("port", 2456, "游戏端口，同时监听该端口和下一端口")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	game, query, err := StartHost(ctx, *bind, *port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听失败: %v\n", err)
		return 1
	}
	fmt.Printf("listening %s and %s\n", game, query)
	<-ctx.Done()
	return 0
}

func cmdClient(args []string) int {
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := fs.String("host", "", "对端 IP")
	port := fs.Int("port", 2456, "对端端口")
	pps := fs.Int("pps", 20, "每秒包数")
	size := fs.Int("size", 0, "固定长度，0 表示在 64 到 1200 之间随机")
	duration := fs.Duration("duration", 10*time.Second, "发送时长")
	session := fs.Uint("session", 0, "会话号，0 表示随机")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *host == "" {
		fmt.Fprintln(os.Stderr, "client 需要 --host")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rep, err := RunClient(ctx, ClientConfig{
		Host:     *host,
		Port:     *port,
		PPS:      *pps,
		Size:     *size,
		Duration: *duration,
		Session:  uint32(*session),
	})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "发送失败: %v\n", err)
		fmt.Println(rep)
		return 1
	}
	fmt.Println(rep)
	return 0
}

func cmdRelay(args []string) int {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	count := fs.Int("count", 20, "每个方向的包数")
	size := fs.Int("size", 128, "模拟包长度")
	insecure := fs.Bool("insecure", false, "关闭 MAC")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, b, err := RunRelayPair(ctx, RelayPairConfig{Packets: *count, Size: *size, Insecure: *insecure})
	fmt.Printf("peer1 %s\n", a)
	fmt.Printf("peer2 %s\n", b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay: %v\n", err)
		return 1
	}
	return 0
}

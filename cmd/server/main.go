package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Q-xuan/GameLink/internal/config"
	"github.com/Q-xuan/GameLink/internal/control"
	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/internal/room"
)

const version = "0.1.0"

func main() {
	cfg, err := config.LoadServer(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "gamelink-server: %v\n", err)
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "gamelink-server: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config.Server) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	logger := log.New(os.Stderr, "gamelink-server ", log.LstdFlags)
	mac := "required"
	if cfg.Insecure {
		mac = "disabled"
	}
	logger.Printf("version %s control=%s relay=%s mac=%s", version, cfg.ControlListen, cfg.RelayListen, mac)

	ln, err := net.Listen("tcp", cfg.ControlListen)
	if err != nil {
		return err
	}
	ua, err := net.ResolveUDPAddr("udp4", cfg.RelayListen)
	if err != nil {
		_ = ln.Close()
		return err
	}
	uc, err := net.ListenUDP("udp4", ua)
	if err != nil {
		_ = ln.Close()
		return err
	}

	rooms := room.NewStore()
	hub := control.NewHub(rooms)
	ctrl := control.New(control.Options{
		Rooms:            rooms,
		Hub:              hub,
		PublicRelay:      cfg.PublicRelay,
		PublicControlURL: cfg.PublicControlURL,
		Logger:           logger,
	})
	fwd := relay.New(relay.Options{
		Rooms:       rooms,
		Observer:    hub,
		Insecure:    cfg.Insecure,
		IdleTimeout: relay.IdleTimeout,
		Logger:      logger,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)
	go func() { errCh <- ctrl.Serve(ctx, ln) }()
	go func() { errCh <- fwd.Serve(ctx, uc) }()
	select {
	case err := <-errCh:
		stop()
		_ = ln.Close()
		_ = uc.Close()
		return err
	case <-ctx.Done():
		_ = ln.Close()
		_ = uc.Close()
		<-errCh
		<-errCh
		logger.Printf("stopped")
		return nil
	}
}

package main

import (
	"context"
	"net"
	"strconv"
)

// StartHost listens on port and port+1. A zero port asks the kernel for two ephemeral ports.
func StartHost(ctx context.Context, bind string, port int) (gameAddr, queryAddr string, err error) {
	if bind == "" {
		bind = "0.0.0.0"
	}
	game, err := listenUDP(bind, port)
	if err != nil {
		return "", "", err
	}
	queryPort := 0
	if port != 0 {
		queryPort = port + 1
	}
	query, err := listenUDP(bind, queryPort)
	if err != nil {
		_ = game.Close()
		return "", "", err
	}
	go func() {
		<-ctx.Done()
		_ = game.Close()
		_ = query.Close()
	}()
	go serveHost(game)
	go serveHost(query)
	return game.LocalAddr().String(), query.LocalAddr().String(), nil
}

func listenUDP(bind string, port int) (*net.UDPConn, error) {
	ua, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return net.ListenUDP("udp4", ua)
}

func serveHost(conn *net.UDPConn) {
	buf := make([]byte, MaxSize+128)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt, err := Decode(buf[:n])
		if err != nil || pkt.Kind != KindData {
			continue
		}
		raw, err := Encode(Packet{
			SessionID: pkt.SessionID,
			Sequence:  pkt.Sequence,
			Timestamp: pkt.Timestamp,
			AckSeq:    pkt.Sequence,
			Kind:      KindAck,
		}, MinSize)
		if err != nil {
			continue
		}
		_, _ = conn.WriteToUDP(raw, addr)
	}
}

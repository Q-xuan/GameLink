package main

import (
	"os"
	"strings"
	"testing"
)

func TestGUIDoesNotDisableMACOrRequireDirect(t *testing.T) {
	b, err := os.ReadFile("gui_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, bad := range []string{"--insecure", "Insecure: true", "必须直连", "必须走直连", "请改为直连"} {
		if strings.Contains(src, bad) {
			t.Fatalf("gui contains %s", bad)
		}
	}
	for _, need := range []string{
		"创建房间", "加入房间", "复制",
		"proxyhint.Advice", "proxyhint.HandshakeDropped", "proxyhint.Rules()",
		"config.DefaultControlURL()", "control.CreateRoom", "control.JoinRoom",
		"relay.Dial", "runTunnel",
	} {
		if !strings.Contains(src, need) {
			t.Fatalf("gui missing %s", need)
		}
	}
}

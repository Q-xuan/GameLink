package proxyhint

import (
	"net"
	"strings"
	"testing"
)

func TestRulesExact(t *testing.T) {
	lines := strings.Split(Rules(), "\n")
	if len(lines) != 2 || lines[0] != RuleProcess || lines[1] != RuleRelay {
		t.Fatalf("%q", Rules())
	}
	if lines[0] != "PROCESS-NAME,gamelink.exe,DIRECT" {
		t.Fatal(lines[0])
	}
	if lines[1] != "IP-CIDR,195.72.187.81/32,DIRECT,no-resolve" {
		t.Fatal(lines[1])
	}
}

func TestCopyIsRecommendation(t *testing.T) {
	for _, s := range []string{Advice, Detected, HandshakeDropped} {
		for _, bad := range []string{"必须直连", "必须走直连", "请改为直连", "需要直连", "只能直连"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%q contains %s", s, bad)
			}
		}
	}
	if !strings.Contains(Advice, "建议直连") || !strings.Contains(Advice, "Hysteria2") || !strings.Contains(Advice, "TUIC") {
		t.Fatal(Advice)
	}
	if !strings.Contains(Advice, "必须转发 UDP") {
		t.Fatal(Advice)
	}
	if strings.Count(HandshakeDropped, "。") != 1 {
		t.Fatalf("want one sentence: %s", HandshakeDropped)
	}
	if !strings.Contains(HandshakeDropped, "UDP") {
		t.Fatal(HandshakeDropped)
	}
	if !strings.Contains(Detected, "最前面") || !strings.Contains(Detected, "不会修改代理配置") {
		t.Fatal(Detected)
	}
}

func TestFakeIPRange(t *testing.T) {
	yes := &net.IPNet{IP: net.ParseIP("198.18.0.1")}
	edge := &net.IPNet{IP: net.ParseIP("198.19.255.255")}
	no := &net.IPNet{IP: net.ParseIP("198.20.0.1")}
	vip := &net.IPNet{IP: net.ParseIP("10.66.0.1")}
	if !ContainsFakeIP([]net.Addr{yes, edge}) {
		t.Fatal("expected fake-ip")
	}
	if ContainsFakeIP([]net.Addr{no, vip}) {
		t.Fatal("unexpected fake-ip")
	}
}

func TestProcessNames(t *testing.T) {
	for _, name := range []string{"FlClash.exe", "clash-verge.exe", "verge-mihomo.exe", "mihomo.exe", "MIHOMO.EXE"} {
		if !matchProcess(name) {
			t.Fatal(name)
		}
	}
	if matchProcess("gamelink.exe") || matchProcess("chrome.exe") {
		t.Fatal("unrelated process")
	}
}

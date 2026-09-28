// Package proxyhint describes an optional DIRECT recommendation.
// It never edits a proxy, a Clash file, or proxy registry keys.
package proxyhint

import (
	"net"
	"net/netip"
	"strings"
)

const (
	// RuleProcess and RuleRelay are the only two lines the copy button emits.
	RuleProcess = "PROCESS-NAME,gamelink.exe,DIRECT"
	RuleRelay   = "IP-CIDR,195.72.187.81/32,DIRECT,no-resolve"

	// Advice is always shown. DIRECT is a recommendation. A UDP-capable
	// proxy node is also fine.
	Advice = "建议直连，也可以用能转发 UDP 的节点（例如 Hysteria2、TUIC）。走代理时节点必须转发 UDP，否则握手会停住。"

	// Detected is shown with the two rules when a known TUN is present.
	Detected = "检测到代理 TUN。可以把下面两条规则放在代理规则列表最前面。到 gamelink.aruyx.com 的 HTTPS 可以继续走代理。程序不会修改代理配置。"

	// HandshakeDropped is the single sentence shown when the handshake
	// does not finish before its deadline.
	HandshakeDropped = "握手没有完成，若正在使用代理，节点可能丢弃了 UDP。"
)

// FakeIP is the carrier-grade NAT range used by fake-ip TUN proxies.
var FakeIP = netip.MustParsePrefix("198.18.0.0/15")

var proxyProcesses = []string{
	"FlClash.exe",
	"clash-verge.exe",
	"verge-mihomo.exe",
	"mihomo.exe",
}

// Rules is the exact text copied to the clipboard, two lines.
func Rules() string {
	return RuleProcess + "\n" + RuleRelay
}

// LocalFakeIP reports whether any local address sits in 198.18.0.0/15.
func LocalFakeIP() bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if FakeIP.Contains(addrIP(a)) {
			return true
		}
	}
	return false
}

// ContainsFakeIP is the pure check used by tests.
func ContainsFakeIP(addrs []net.Addr) bool {
	for _, a := range addrs {
		if FakeIP.Contains(addrIP(a)) {
			return true
		}
	}
	return false
}

func addrIP(a net.Addr) netip.Addr {
	if a == nil {
		return netip.Addr{}
	}
	switch v := a.(type) {
	case *net.IPNet:
		return unmap(v.IP)
	case *net.IPAddr:
		return unmap(v.IP)
	default:
		host, _, err := net.SplitHostPort(a.String())
		if err != nil {
			host = a.String()
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return netip.Addr{}
		}
		return ip.Unmap()
	}
}

func unmap(ip net.IP) netip.Addr {
	parsed, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}
	}
	return parsed.Unmap()
}

func matchProcess(name string) bool {
	for _, want := range proxyProcesses {
		if strings.EqualFold(name, want) {
			return true
		}
	}
	return false
}

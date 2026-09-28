// Package route describes the single on-link prefix the client may install.
// Applying it is Windows-only. Nothing here touches DNS or any other adapter.
package route

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/Q-xuan/GameLink/internal/peer"
)

const (
	// AdapterName matches tun.AdapterName.
	AdapterName = "GameLink"
	// MTU matches tun.MTU.
	MTU = 1280
)

// Route is one prefix installed on the GameLink adapter.
type Route struct {
	Prefix netip.Prefix
	OnLink bool
}

// Plan is everything this process may add to the routing table.
// Routes contains only 10.66.0.0/24 on-link. There is no default route and no DNS.
type Plan struct {
	Adapter string
	Address netip.Prefix
	MTU     int
	Routes  []Route
}

// ForVIP builds the plan for one assigned virtual address, such as 10.66.0.1/24.
func ForVIP(vip netip.Addr) (Plan, error) {
	if !vip.Is4() {
		return Plan{}, errors.New("虚拟地址必须是 IPv4")
	}
	id := uint32(vip.As4()[3])
	got, ok := peer.VirtualIP(id)
	if !ok || got != vip {
		return Plan{}, fmt.Errorf("虚拟地址 %s 不在 10.66.0.1–8", vip)
	}
	address := netip.PrefixFrom(vip, 24)
	if !address.IsValid() {
		return Plan{}, fmt.Errorf("无法表示 %s/24", vip)
	}
	return Plan{
		Adapter: AdapterName,
		Address: address,
		MTU:     MTU,
		Routes: []Route{{
			Prefix: netip.MustParsePrefix("10.66.0.0/24"),
			OnLink: true,
		}},
	}, nil
}

// HasDefaultRoute reports a 0.0.0.0/0 or ::/0 entry.
func (p Plan) HasDefaultRoute() bool {
	for _, r := range p.Routes {
		if r.Prefix.Bits() == 0 {
			return true
		}
	}
	return false
}

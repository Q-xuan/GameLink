package route

import (
	"net/netip"
	"testing"
)

func TestPlanOnlyOnLinkSubnet(t *testing.T) {
	for _, vip := range []string{"10.66.0.1", "10.66.0.8"} {
		plan, err := ForVIP(netip.MustParseAddr(vip))
		if err != nil {
			t.Fatal(err)
		}
		if plan.Adapter != "GameLink" || plan.MTU != 1280 {
			t.Fatalf("%+v", plan)
		}
		if plan.Address.String() != vip+"/24" {
			t.Fatalf("address %s", plan.Address)
		}
		if len(plan.Routes) != 1 {
			t.Fatalf("routes %+v", plan.Routes)
		}
		r := plan.Routes[0]
		if r.Prefix.String() != "10.66.0.0/24" || !r.OnLink {
			t.Fatalf("route %+v", r)
		}
		if plan.HasDefaultRoute() {
			t.Fatal("default route")
		}
		for _, rt := range plan.Routes {
			if rt.Prefix == netip.MustParsePrefix("0.0.0.0/0") || rt.Prefix == netip.MustParsePrefix("::/0") {
				t.Fatalf("default %s", rt.Prefix)
			}
			if rt.Prefix.Bits() == 0 {
				t.Fatal("zero-length prefix")
			}
		}
	}
}

func TestPlanRejectsOutsideSubnet(t *testing.T) {
	for _, vip := range []string{"10.66.0.0", "10.66.0.9", "10.66.1.1", "0.0.0.0", "192.168.1.1"} {
		if _, err := ForVIP(netip.MustParseAddr(vip)); err == nil {
			t.Fatalf("accepted %s", vip)
		}
	}
}

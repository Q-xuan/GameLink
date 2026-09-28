//go:build windows

package route

import (
	"errors"
	"fmt"
	"net/netip"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ipDadStatePreferred = 4
	routeMetric         = 1
)

// rawSockaddrInet matches SOCKADDR_INET. The IPv4 address is at byte offset 4.
type rawSockaddrInet struct {
	Family uint16
	Port   uint16
	Data   [6]uint32
}

// ipAddressPrefix matches IP_ADDRESS_PREFIX.
type ipAddressPrefix struct {
	Prefix       rawSockaddrInet
	PrefixLength uint8
}

// mibIpForwardRow2 matches MIB_IPFORWARD_ROW2.
type mibIpForwardRow2 struct {
	InterfaceLuid        uint64
	InterfaceIndex       uint32
	DestinationPrefix    ipAddressPrefix
	NextHop              rawSockaddrInet
	SitePrefixLength     uint8
	ValidLifetime        uint32
	PreferredLifetime    uint32
	Metric               uint32
	Protocol             uint32
	Loopback             uint8
	AutoconfigureAddress uint8
	Publish              uint8
	Immortal             uint8
	Age                  uint32
	Origin               uint32
}

var (
	modiphlpapi                         = windows.NewLazySystemDLL("iphlpapi.dll")
	procInitializeUnicastIpAddressEntry = modiphlpapi.NewProc("InitializeUnicastIpAddressEntry")
	procCreateUnicastIpAddressEntry     = modiphlpapi.NewProc("CreateUnicastIpAddressEntry")
	procDeleteUnicastIpAddressEntry     = modiphlpapi.NewProc("DeleteUnicastIpAddressEntry")
	procInitializeIpForwardEntry        = modiphlpapi.NewProc("InitializeIpForwardEntry")
	procCreateIpForwardEntry2           = modiphlpapi.NewProc("CreateIpForwardEntry2")
	procDeleteIpForwardEntry2           = modiphlpapi.NewProc("DeleteIpForwardEntry2")
	procGetIpInterfaceEntry             = modiphlpapi.NewProc("GetIpInterfaceEntry")
	procSetIpInterfaceEntry             = modiphlpapi.NewProc("SetIpInterfaceEntry")
)

// Install applies plan to the adapter identified by luid.
// The returned function removes only the address and routes this call added.
func Install(luid uint64, vip netip.Addr) (func(), error) {
	plan, err := ForVIP(vip)
	if err != nil {
		return nil, err
	}
	if luid == 0 {
		return nil, errors.New("网卡 LUID 无效，拒绝改路由")
	}
	if err := refuseUnsafe(plan); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for {
		last = apply(luid, plan)
		if last == nil {
			break
		}
		if !transient(last) || time.Now().After(deadline) {
			return nil, last
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() { remove(luid, plan) }, nil
}

func refuseUnsafe(plan Plan) error {
	if plan.HasDefaultRoute() {
		return errors.New("拒绝安装默认路由")
	}
	for _, r := range plan.Routes {
		if !r.OnLink || r.Prefix.Bits() == 0 {
			return fmt.Errorf("拒绝安装路由 %s", r.Prefix)
		}
	}
	return nil
}

func apply(luid uint64, plan Plan) error {
	if err := setMTU(luid, plan.MTU); err != nil {
		return fmt.Errorf("设置 MTU: %w", err)
	}
	if err := addAddress(luid, plan.Address); err != nil && !alreadyExists(err) {
		return fmt.Errorf("添加地址 %s: %w", plan.Address, err)
	}
	for _, r := range plan.Routes {
		if err := addOnLink(luid, r.Prefix); err != nil && !alreadyExists(err) {
			_ = deleteAddress(luid, plan.Address)
			return fmt.Errorf("添加路由 %s: %w", r.Prefix, err)
		}
	}
	return nil
}

func remove(luid uint64, plan Plan) {
	for i := len(plan.Routes) - 1; i >= 0; i-- {
		_ = deleteOnLink(luid, plan.Routes[i].Prefix)
	}
	_ = deleteAddress(luid, plan.Address)
}

func setMTU(luid uint64, mtu int) error {
	row := windows.MibIpInterfaceRow{
		Family:        windows.AF_INET,
		InterfaceLuid: luid,
	}
	if err := ipErr(procGetIpInterfaceEntry, uintptr(unsafe.Pointer(&row))); err != nil {
		return err
	}
	row.NlMtu = uint32(mtu)
	row.DisableDefaultRoutes = 1
	row.SitePrefixLength = 0
	return ipErr(procSetIpInterfaceEntry, uintptr(unsafe.Pointer(&row)))
}

func addAddress(luid uint64, prefix netip.Prefix) error {
	var row windows.MibUnicastIpAddressRow
	procInitializeUnicastIpAddressEntry.Call(uintptr(unsafe.Pointer(&row)))
	putIPv4In6(&row.Address, prefix.Addr())
	row.InterfaceLuid = luid
	row.OnLinkPrefixLength = uint8(prefix.Bits())
	row.DadState = ipDadStatePreferred
	row.SkipAsSource = 0
	return ipErr(procCreateUnicastIpAddressEntry, uintptr(unsafe.Pointer(&row)))
}

func deleteAddress(luid uint64, prefix netip.Prefix) error {
	var row windows.MibUnicastIpAddressRow
	putIPv4In6(&row.Address, prefix.Addr())
	row.InterfaceLuid = luid
	return ipErr(procDeleteUnicastIpAddressEntry, uintptr(unsafe.Pointer(&row)))
}

func addOnLink(luid uint64, prefix netip.Prefix) error {
	row := onLinkRow(luid, prefix)
	return ipErr(procCreateIpForwardEntry2, uintptr(unsafe.Pointer(&row)))
}

func deleteOnLink(luid uint64, prefix netip.Prefix) error {
	row := onLinkRow(luid, prefix)
	return ipErr(procDeleteIpForwardEntry2, uintptr(unsafe.Pointer(&row)))
}

func onLinkRow(luid uint64, prefix netip.Prefix) mibIpForwardRow2 {
	var row mibIpForwardRow2
	procInitializeIpForwardEntry.Call(uintptr(unsafe.Pointer(&row)))
	row.InterfaceLuid = luid
	putPrefix(&row.DestinationPrefix, prefix)
	row.NextHop.Family = windows.AF_INET
	row.Metric = routeMetric
	return row
}

func putPrefix(dst *ipAddressPrefix, prefix netip.Prefix) {
	putIPv4(&dst.Prefix, prefix.Masked().Addr())
	dst.PrefixLength = uint8(prefix.Bits())
}

func putIPv4(sa *rawSockaddrInet, ip netip.Addr) {
	*sa = rawSockaddrInet{Family: windows.AF_INET}
	b := ip.As4()
	*(*[4]byte)(unsafe.Add(unsafe.Pointer(sa), 4)) = b
}

func putIPv4In6(sa *windows.RawSockaddrInet6, ip netip.Addr) {
	*sa = windows.RawSockaddrInet6{Family: windows.AF_INET}
	b := ip.As4()
	*(*[4]byte)(unsafe.Add(unsafe.Pointer(sa), 4)) = b
}

func ipErr(p *windows.LazyProc, arg uintptr) error {
	r, _, _ := p.Call(arg)
	if r == 0 {
		return nil
	}
	return windows.Errno(r)
}

func alreadyExists(err error) bool {
	return errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

func transient(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_NO_SUCH_DEVICE)
}

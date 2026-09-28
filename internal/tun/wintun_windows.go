//go:build windows

package tun

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

// ringCapacity is 4 MiB, a power of two inside Wintun's allowed range.
const ringCapacity = 0x400000

type winDevice struct {
	session wintun.Session
	once    sync.Once
	closed  chan struct{}
}

// Open creates the GameLink Wintun adapter and starts a session.
// The returned LUID is the interface this process may configure.
// closeAdapter removes that adapter; call it after the address and route are gone.
// wintun.dll is loaded from the executable directory.
func Open() (Device, uint64, func(), error) {
	adapter, err := wintun.CreateAdapter(AdapterName, "Wintun", nil)
	if err != nil {
		return nil, 0, nil, explainWintun(err)
	}
	session, err := adapter.StartSession(ringCapacity)
	if err != nil {
		_ = adapter.Close()
		return nil, 0, nil, fmt.Errorf("启动 %s 会话失败: %w", AdapterName, err)
	}
	dev := &winDevice{
		session: session,
		closed:  make(chan struct{}),
	}
	var once sync.Once
	closeAdapter := func() {
		once.Do(func() {
			_ = dev.Close()
			_ = adapter.Close()
		})
	}
	return dev, adapter.LUID(), closeAdapter, nil
}

func (d *winDevice) Read() ([]byte, error) {
	for {
		select {
		case <-d.closed:
			return nil, net.ErrClosed
		default:
		}
		packet, err := d.session.ReceivePacket()
		if err == nil {
			out := append([]byte(nil), packet...)
			if len(packet) > 0 {
				d.session.ReleaseReceivePacket(packet)
			}
			return out, nil
		}
		if !errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			select {
			case <-d.closed:
				return nil, net.ErrClosed
			default:
			}
			return nil, err
		}
		ev := d.session.ReadWaitEvent()
		_, _ = windows.WaitForSingleObject(ev, windows.INFINITE)
	}
}

func (d *winDevice) Write(pkt []byte) (int, error) {
	select {
	case <-d.closed:
		return 0, net.ErrClosed
	default:
	}
	if len(pkt) == 0 || len(pkt) > wintun.PacketSizeMax {
		return 0, nil
	}
	buf, err := d.session.AllocateSendPacket(len(pkt))
	if err != nil {
		select {
		case <-d.closed:
			return 0, net.ErrClosed
		default:
		}
		return 0, err
	}
	copy(buf, pkt)
	d.session.SendPacket(buf)
	return len(pkt), nil
}

func (d *winDevice) Close() error {
	d.once.Do(func() {
		close(d.closed)
		d.session.End()
	})
	return nil
}

func (d *winDevice) MTU() int { return MTU }

func explainWintun(err error) error {
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "wintun") || strings.Contains(lower, "load") || strings.Contains(lower, "dll") {
		return fmt.Errorf("无法加载程序自带的 wintun.dll。请重新双击 gamelink.exe，让它在自身旁边写出官方 Wintun 0.14.1 amd64，并在 UAC 提示中允许: %w", err)
	}
	return fmt.Errorf("创建网卡 %s 失败: %w", AdapterName, err)
}

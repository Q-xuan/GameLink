//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Q-xuan/GameLink/internal/config"
	"github.com/Q-xuan/GameLink/internal/control"
	"github.com/Q-xuan/GameLink/internal/invite"
	"github.com/Q-xuan/GameLink/internal/proxyhint"
	"github.com/Q-xuan/GameLink/internal/relay"
	"github.com/Q-xuan/GameLink/internal/room"
	"github.com/Q-xuan/GameLink/internal/tun"
	"github.com/Q-xuan/GameLink/internal/winproto"
	"github.com/Q-xuan/GameLink/internal/wintunbin"
)

const guiAvailable = true

// handshakeTimeout is how long the window waits before saying UDP may be dropped.
// The CLI keeps the 3 second default because it does not set a deadline.
const handshakeTimeout = 15 * time.Second

const (
	phaseIdle = iota
	phaseHandshake
	phasePing
	phaseFailed
)

const (
	idCopyInvite = 1001
	idCreate     = 1002
	idJoin       = 1003
	idCopyRules  = 1004

	wmDestroy  = 0x0002
	wmClose    = 0x0010
	wmCommand  = 0x0111
	wmTimer    = 0x0113
	wmSetFont  = 0x0030
	wmCopyData = 0x004A

	wsOverlapped = 0x00000000
	wsCaption    = 0x00C00000
	wsSysMenu    = 0x00080000
	wsMinimize   = 0x00020000
	wsVisible    = 0x10000000
	wsChild      = 0x40000000
	wsTabStop    = 0x00010000
	wsBorder     = 0x00800000

	wsExClientEdge = 0x00000200

	esAutoHScroll = 0x0080
	esReadOnly    = 0x0800
	esMultiLine   = 0x0004
	esAutoVScroll = 0x0040

	swRestore = 9
	swShow    = 5

	bnClicked = 0
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateWindow        = user32.NewProc("UpdateWindow")
	procSetWindowTextW      = user32.NewProc("SetWindowTextW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procSetTimer            = user32.NewProc("SetTimer")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procEnableWindow        = user32.NewProc("EnableWindow")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procCloseClipboard      = user32.NewProc("CloseClipboard")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procCreateFontW  = gdi32.NewProc("CreateFontW")

	className     = mustUTF16("GameLinkWindow")
	wndProcHandle = windows.NewCallback(wndProc)
	instanceMutex windows.Handle
	app           *guiApp
)

type guiApp struct {
	hwnd       uintptr
	statusHw   uintptr
	vipHw      uintptr
	latencyHw  uintptr
	adviceHw   uintptr
	linkHw     uintptr
	hintHw     uintptr
	rulesHw    uintptr
	inviteHw   uintptr
	joinHw     uintptr
	createHw   uintptr
	joinBtnHw  uintptr
	copyInvHw  uintptr
	copyRuleHw uintptr

	showRules bool

	mu          sync.Mutex
	closed      bool
	cancel      context.CancelFunc
	gen         int
	done        chan struct{}
	phase       int
	status      string
	vip         string
	link        string
	invite      string
	busy        bool
	handshakeAt time.Time
	session     atomic.Pointer[relay.Session]
	applied     map[uintptr]string
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type copyData struct {
	Data uintptr
	Size uint32
	Ptr  uintptr
}

func runGUI(initial string) int {
	hideConsole()
	enableDPI()
	initControls()
	if err := wintunbin.Install(); err != nil {
		messageBox(err.Error(), "GameLink")
		return 1
	}
	if err := tun.RequireAdmin(); err != nil {
		messageBox(err.Error(), "GameLink")
		return 1
	}
	note := ""
	if err := winproto.RegisterCurrent(); err != nil {
		note = "注册 gamelink:// 失败，仍可粘贴邀请串。"
	}
	if forwardExisting(initial) {
		return 0
	}
	app = &guiApp{
		showRules: proxyhint.Present(),
		status:    "未连接",
		vip:       "—",
		link:      "尚未握手",
		invite:    "",
		applied:   map[uintptr]string{},
	}
	if note != "" {
		app.link = note
	}
	if err := openWindow(); err != nil {
		messageBox(err.Error(), "GameLink")
		return 1
	}
	if initial != "" {
		setText(app.joinHw, initial)
		app.onJoin(initial)
	}
	loop()
	app.mu.Lock()
	done := app.done
	app.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	return 0
}

func forwardExisting(initial string) bool {
	name := mustUTF16(`Local\GameLinkGUI`)
	handle, err := windows.CreateMutex(nil, false, name)
	if err == nil {
		instanceMutex = handle
		return false
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return false
	}
	hwnd := findMain()
	if hwnd == 0 {
		messageBox("GameLink 已经在运行。", "GameLink")
		return true
	}
	if initial != "" {
		sendCopy(hwnd, initial)
	}
	procShowWindow.Call(hwnd, swRestore)
	procSetForegroundWindow.Call(hwnd)
	return true
}

func findMain() uintptr {
	for i := 0; i < 20; i++ {
		h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
		if h != 0 {
			return h
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0
}

func sendCopy(hwnd uintptr, text string) {
	b := append([]byte(text), 0)
	cds := copyData{Data: 1, Size: uint32(len(b)), Ptr: uintptr(unsafe.Pointer(&b[0]))}
	procSendMessageW.Call(hwnd, wmCopyData, 0, uintptr(unsafe.Pointer(&cds)))
	runtime.KeepAlive(b)
	runtime.KeepAlive(&cds)
}

func openWindow() error {
	inst, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := procLoadCursorW.Call(0, 32512)
	cls := wndClassEx{
		WndProc:    wndProcHandle,
		Instance:   inst,
		Cursor:     cursor,
		Background: 6,
		ClassName:  className,
	}
	cls.Size = uint32(unsafe.Sizeof(cls))
	atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls)))
	if atom == 0 {
		return errors.New("无法注册窗口")
	}
	height := uintptr(540)
	if app.showRules {
		height = 760
	}
	title := mustUTF16("GameLink")
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsOverlapped|wsCaption|wsSysMenu|wsMinimize,
		0x80000000, 0x80000000, 760, height,
		0, 0, inst, 0,
	)
	runtime.KeepAlive(title)
	runtime.KeepAlive(className)
	if hwnd == 0 {
		return errors.New("无法创建窗口")
	}
	app.hwnd = hwnd
	app.buildControls()
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	procSetTimer.Call(hwnd, 1, 200, 0)
	app.refresh()
	return nil
}

func (a *guiApp) buildControls() {
	const (
		label = wsChild | wsVisible
		edit  = wsChild | wsVisible | wsTabStop | wsBorder | esAutoHScroll
		btn   = wsChild | wsVisible | wsTabStop
	)
	a.statusHw = child("STATIC", "", label, 0, 20, 16, 710, 24, 0)
	a.vipHw = child("STATIC", "", label, 0, 20, 44, 710, 24, 0)
	a.latencyHw = child("STATIC", "", label, 0, 20, 72, 710, 24, 0)
	a.adviceHw = child("STATIC", proxyhint.Advice, label, 0, 20, 104, 710, 64, 0)
	a.linkHw = child("STATIC", "", label, 0, 20, 174, 710, 48, 0)
	a.inviteHw = child("EDIT", "", edit|esReadOnly, wsExClientEdge, 20, 252, 560, 28, 0)
	a.copyInvHw = child("BUTTON", "复制", btn, 0, 592, 250, 120, 30, idCopyInvite)
	joinLabel := child("STATIC", "加入（粘贴邀请串）", label, 0, 20, 292, 710, 22, 0)
	a.joinHw = child("EDIT", "", edit, wsExClientEdge, 20, 318, 700, 28, 0)
	a.createHw = child("BUTTON", "创建房间", btn, 0, 20, 362, 160, 34, idCreate)
	a.joinBtnHw = child("BUTTON", "加入房间", btn, 0, 196, 362, 160, 34, idJoin)
	if a.showRules {
		a.hintHw = child("STATIC", proxyhint.Detected, label, 0, 20, 414, 710, 72, 0)
		rules := strings.ReplaceAll(proxyhint.Rules(), "\n", "\r\n")
		a.rulesHw = child("EDIT", rules, edit|esReadOnly|esMultiLine|esAutoVScroll, wsExClientEdge, 20, 492, 700, 52, 0)
		a.copyRuleHw = child("BUTTON", "复制", btn, 0, 20, 556, 120, 30, idCopyRules)
	}
	inviteLabel := child("STATIC", "邀请串", label, 0, 20, 228, 200, 22, 0)
	font := uiFont()
	for _, h := range []uintptr{
		a.statusHw, a.vipHw, a.latencyHw, a.adviceHw, a.linkHw, inviteLabel, joinLabel,
		a.inviteHw, a.copyInvHw, a.joinHw, a.createHw, a.joinBtnHw,
		a.hintHw, a.rulesHw, a.copyRuleHw,
	} {
		if h != 0 && font != 0 {
			procSendMessageW.Call(h, wmSetFont, font, 1)
		}
	}
}

func child(class, text string, style, ex uintptr, x, y, w, h int, id uintptr) uintptr {
	cls := mustUTF16(class)
	txt := mustUTF16(text)
	r, _, _ := procCreateWindowExW.Call(
		ex,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(txt)),
		style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		app.hwnd, id, 0, 0,
	)
	runtime.KeepAlive(cls)
	runtime.KeepAlive(txt)
	return r
}

func loop() {
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmDestroy:
		if app != nil {
			app.mu.Lock()
			app.closed = true
			if app.cancel != nil {
				app.cancel()
			}
			app.mu.Unlock()
		}
		procPostQuitMessage.Call(0)
		return 0
	case wmCommand:
		if hiword(wparam) == bnClicked && app != nil {
			switch loword(wparam) {
			case idCreate:
				app.onCreate()
			case idJoin:
				app.onJoin(windowText(app.joinHw))
			case idCopyInvite:
				app.copyInvite()
			case idCopyRules:
				app.copyRules()
			}
		}
		return 0
	case wmTimer:
		if app != nil {
			app.refresh()
		}
		return 0
	case wmCopyData:
		if app != nil {
			app.onCopyData(lparam)
		}
		return 1
	case wmClose:
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func (a *guiApp) onCopyData(lparam uintptr) {
	if lparam == 0 {
		return
	}
	cds := (*copyData)(unsafe.Pointer(lparam))
	if cds.Ptr == 0 || cds.Size == 0 {
		return
	}
	b := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(cds.Ptr)), cds.Size)...)
	text := strings.TrimRight(string(b), "\x00")
	setText(a.joinHw, text)
	a.onJoin(text)
}

func (a *guiApp) onCreate() {
	a.start(func(ctx context.Context, gen int) {
		a.setStatus(gen, phaseIdle, "正在创建房间", "正在连接控制面")
		created, err := control.CreateRoom(ctx, config.DefaultControlURL())
		if err != nil {
			if ctx.Err() == nil {
				a.fail(gen, "创建房间失败", err.Error())
			}
			return
		}
		a.setInvite(gen, invite.Format(created.Code, created.Token))
		a.runRoom(ctx, gen, created.Code, created.Token, created.RoomID, created.VIP, created.Relay, created.PeerID)
	})
}

func (a *guiApp) onJoin(text string) {
	code, token, err := invite.Parse(text)
	if err != nil {
		a.mu.Lock()
		a.phase = phaseFailed
		a.busy = false
		a.status = "邀请串无效"
		a.link = err.Error()
		a.mu.Unlock()
		return
	}
	a.start(func(ctx context.Context, gen int) {
		a.setStatus(gen, phaseIdle, "正在加入房间", "正在连接控制面")
		joined, err := control.JoinRoom(ctx, config.DefaultControlURL(), code, token)
		if err != nil {
			if ctx.Err() == nil {
				a.fail(gen, "加入房间失败", err.Error())
			}
			return
		}
		a.runRoom(ctx, gen, joined.Code, token, joined.RoomID, joined.VIP, joined.Relay, joined.PeerID)
	})
}

func (a *guiApp) runRoom(ctx context.Context, gen int, code, token, roomText, vip, relayAddr string, peerID uint32) {
	a.setVIP(gen, vip)
	if !a.beginHandshake(gen) {
		return
	}
	tok, err := room.ParseToken(token)
	if err != nil {
		a.fail(gen, "令牌无效", err.Error())
		return
	}
	roomID, err := strconv.ParseUint(roomText, 10, 64)
	if err != nil {
		a.fail(gen, "房间号无效", err.Error())
		return
	}
	sess, err := relay.Dial(relay.SessionConfig{
		Relay:  relayAddr,
		RoomID: roomID,
		PeerID: peerID,
		Token:  tok,
	})
	if err != nil {
		a.fail(gen, "连接中继失败", err.Error())
		return
	}
	defer sess.Close()
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	err = sess.Handshake(hctx)
	cancel()
	if err != nil {
		if ctx.Err() != nil || !a.alive(gen) {
			return
		}
		if errors.Is(err, relay.ErrHandshakeTimeout) {
			a.fail(gen, "握手未完成", proxyhint.HandshakeDropped)
			return
		}
		a.fail(gen, "握手失败", err.Error())
		return
	}
	if !a.enterPing(gen, sess) {
		return
	}
	defer a.session.CompareAndSwap(sess, nil)
	go streamEvents(ctx, config.DefaultControlURL(), code, token, peerID)
	go func() { _ = sess.Keepalive(ctx) }()
	if err := runTunnel(ctx, sess, vip); err != nil && ctx.Err() == nil && a.alive(gen) {
		a.fail(gen, "网卡或转发已停止", err.Error())
		return
	}
	if ctx.Err() == nil && a.alive(gen) {
		a.fail(gen, "已断开", "连接已结束")
	}
}

func (a *guiApp) start(fn func(ctx context.Context, gen int)) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	if a.cancel != nil {
		a.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.gen++
	gen := a.gen
	a.cancel = cancel
	a.busy = true
	a.phase = phaseIdle
	prev := a.done
	done := make(chan struct{})
	a.done = done
	a.mu.Unlock()
	go func() {
		defer close(done)
		if prev != nil {
			select {
			case <-prev:
			case <-time.After(5 * time.Second):
			}
		}
		if ctx.Err() != nil {
			return
		}
		fn(ctx, gen)
		a.mu.Lock()
		if gen == a.gen && a.phase != phaseFailed && a.phase != phasePing {
			a.busy = false
		}
		a.mu.Unlock()
	}()
}

func (a *guiApp) beginHandshake(gen int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || gen != a.gen {
		return false
	}
	a.phase = phaseHandshake
	a.busy = true
	a.handshakeAt = time.Now()
	a.status = "正在握手"
	a.link = "正在握手，已等待 0 秒"
	return true
}

func (a *guiApp) enterPing(gen int, sess *relay.Session) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || gen != a.gen {
		return false
	}
	a.session.Store(sess)
	a.phase = phasePing
	a.busy = false
	a.status = "已连接"
	a.link = "已连接，即将 Ping"
	return true
}

func (a *guiApp) setStatus(gen, phase int, status, link string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || gen != a.gen {
		return
	}
	a.phase = phase
	a.status = status
	a.link = link
}

func (a *guiApp) setInvite(gen int, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || gen != a.gen {
		return
	}
	a.invite = text
}

func (a *guiApp) setVIP(gen int, vip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || gen != a.gen {
		return
	}
	if vip == "" {
		vip = "—"
	}
	a.vip = vip
}

func (a *guiApp) fail(gen int, status, link string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || gen != a.gen {
		return
	}
	a.phase = phaseFailed
	a.busy = false
	a.status = status
	a.link = link
}

func (a *guiApp) alive(gen int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.closed && gen == a.gen
}

func (a *guiApp) refresh() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	status := a.status
	vip := a.vip
	link := a.link
	invite := a.invite
	phase := a.phase
	busy := a.busy
	started := a.handshakeAt
	a.mu.Unlock()

	latency := "—"
	switch phase {
	case phaseHandshake:
		secs := int(time.Since(started).Seconds())
		link = fmt.Sprintf("正在握手，已等待 %d 秒", secs)
	case phasePing:
		if s := a.session.Load(); s != nil {
			p := s.Probe()
			switch {
			case p.HasRTT:
				ms := p.RTT.Milliseconds()
				if p.RTT > 0 && ms == 0 {
					latency = "<1 ms"
				} else {
					latency = fmt.Sprintf("%d ms", ms)
				}
				link = "Ping 正常"
			case !p.LastPing.IsZero():
				latency = "测量中"
				link = "正在 Ping，等待 Pong"
			default:
				latency = "测量中"
				link = "已连接，即将 Ping"
			}
		}
	}
	a.put(a.statusHw, "状态："+status)
	a.put(a.vipHw, "本机虚拟地址："+vip)
	a.put(a.latencyHw, "延迟："+latency)
	a.put(a.linkHw, link)
	if invite != "" {
		a.put(a.inviteHw, invite)
	}
	enable(a.createHw, !busy)
	enable(a.joinBtnHw, !busy)
}

func (a *guiApp) put(hwnd uintptr, text string) {
	if hwnd == 0 {
		return
	}
	if a.applied[hwnd] == text {
		return
	}
	a.applied[hwnd] = text
	setText(hwnd, text)
}

func (a *guiApp) copyInvite() {
	a.mu.Lock()
	text := a.invite
	a.mu.Unlock()
	if text == "" {
		text = windowText(a.inviteHw)
	}
	if text == "" {
		a.mu.Lock()
		a.link = "还没有邀请串"
		a.mu.Unlock()
		return
	}
	if err := writeClipboard(text); err != nil {
		a.note("复制失败")
		return
	}
	a.note("邀请串已复制")
}

func (a *guiApp) copyRules() {
	text := strings.ReplaceAll(proxyhint.Rules(), "\n", "\r\n")
	if err := writeClipboard(text); err != nil {
		a.note("复制失败")
		return
	}
	a.note("规则已复制")
}

func (a *guiApp) note(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.phase == phaseHandshake || a.phase == phaseFailed || a.phase == phasePing {
		return
	}
	a.link = text
}

func setText(hwnd uintptr, text string) {
	p := mustUTF16(text)
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

func windowText(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	n, _, _ := procGetWindowTextLength.Call(hwnd)
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}

func enable(hwnd uintptr, on bool) {
	if hwnd == 0 {
		return
	}
	v := uintptr(0)
	if on {
		v = 1
	}
	procEnableWindow.Call(hwnd, v)
}

func writeClipboard(text string) error {
	utf, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	r, _, err := procOpenClipboard.Call(0)
	if r == 0 {
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	h, _, err := procGlobalAlloc.Call(0x0002, uintptr(len(utf)*2))
	if h == 0 {
		return err
	}
	ptr, _, err := procGlobalLock.Call(h)
	if ptr == 0 {
		return err
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(utf))
	copy(dst, utf)
	procGlobalUnlock.Call(h)
	r, _, err = procSetClipboardData.Call(13, h)
	if r == 0 {
		return err
	}
	return nil
}

func messageBox(text, title string) {
	body := mustUTF16(text)
	caption := mustUTF16(title)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(caption)), 0x10)
	runtime.KeepAlive(body)
	runtime.KeepAlive(caption)
}

func hideConsole() {
	get := kernel32.NewProc("GetConsoleWindow")
	hwnd, _, _ := get.Call()
	if hwnd != 0 {
		procShowWindow.Call(hwnd, 0)
	}
}

func enableDPI() {
	user32.NewProc("SetProcessDpiAwarenessContext").Call(^uintptr(3))
}

func initControls() {
	type icc struct {
		size uint32
		icc  uint32
	}
	x := icc{size: 8, icc: 0x00004000}
	windows.NewLazySystemDLL("comctl32.dll").NewProc("InitCommonControlsEx").Call(uintptr(unsafe.Pointer(&x)))
	runtime.KeepAlive(&x)
}

func uiFont() uintptr {
	name := mustUTF16("Microsoft YaHei UI")
	h, _, _ := procCreateFontW.Call(
		^uintptr(15),
		0, 0, 0,
		400,
		0, 0, 0,
		1,
		0, 0, 5, 0,
		uintptr(unsafe.Pointer(name)),
	)
	runtime.KeepAlive(name)
	return h
}

func mustUTF16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return mustUTF16(" ")
	}
	return p
}

func loword(v uintptr) uint16 { return uint16(v) }
func hiword(v uintptr) uint16 { return uint16(v >> 16) }

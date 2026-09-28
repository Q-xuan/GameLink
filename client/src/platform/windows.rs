//! Wintun adapter, on-link route, current-user protocol, and TUN detection.
//! DNS settings and every adapter other than GameLink are left alone.

use std::io;
use std::net::Ipv4Addr;
use std::sync::Arc;
use std::time::{Duration, Instant};

use windows::core::{PCSTR, PCWSTR};
use windows::Win32::Foundation::{
    CloseHandle, GetLastError, BOOLEAN, ERROR_ALREADY_EXISTS, ERROR_FILE_NOT_FOUND,
    ERROR_NOT_FOUND, ERROR_NO_SUCH_DEVICE, ERROR_OBJECT_ALREADY_EXISTS, HANDLE, WAIT_OBJECT_0,
    WIN32_ERROR,
};
use windows::Win32::NetworkManagement::IpHelper::{
    CreateIpForwardEntry2, CreateUnicastIpAddressEntry, DeleteIpForwardEntry2,
    DeleteUnicastIpAddressEntry, GetAdaptersAddresses, GetIpInterfaceEntry,
    InitializeIpForwardEntry, InitializeUnicastIpAddressEntry, SetIpInterfaceEntry,
    GAA_FLAG_SKIP_ANYCAST, GAA_FLAG_SKIP_DNS_SERVER, GAA_FLAG_SKIP_MULTICAST,
    IP_ADAPTER_ADDRESSES_LH, MIB_IPFORWARD_ROW2, MIB_IPINTERFACE_ROW, MIB_UNICASTIPADDRESS_ROW,
};
use windows::Win32::NetworkManagement::Ndis::NET_LUID_LH;
use windows::Win32::Networking::WinSock::{
    IpDadStatePreferred, AF_INET, AF_UNSPEC, SOCKADDR_IN, SOCKADDR_INET,
};
use windows::Win32::System::Diagnostics::ToolHelp::{
    CreateToolhelp32Snapshot, Process32FirstW, Process32NextW, PROCESSENTRY32W, TH32CS_SNAPPROCESS,
};
use windows::Win32::System::LibraryLoader::{GetProcAddress, LoadLibraryW};
use windows::Win32::System::Threading::WaitForSingleObject;
use windows::Win32::UI::Shell::IsUserAnAdmin;

use crate::device::PacketIo;
use crate::plan::{self, Plan};
use crate::proxy::{self, contains_fake_ip};
use crate::wintun_pin;

const RING_CAPACITY: u32 = 0x400000;
const PACKET_MAX: usize = 0xFFFF;
const ERROR_NO_MORE_ITEMS: u32 = 259;
const ERROR_HANDLE_EOF: u32 = 38;
const WAIT_TIMEOUT: u32 = 258;

type CreateAdapter =
    unsafe extern "system" fn(*const u16, *const u16, *const u8) -> *mut std::ffi::c_void;
type CloseAdapter = unsafe extern "system" fn(*mut std::ffi::c_void);
type GetLuid = unsafe extern "system" fn(*mut std::ffi::c_void, *mut NET_LUID_LH);
type StartSession = unsafe extern "system" fn(*mut std::ffi::c_void, u32) -> *mut std::ffi::c_void;
type EndSession = unsafe extern "system" fn(*mut std::ffi::c_void);
type GetReadEvent = unsafe extern "system" fn(*mut std::ffi::c_void) -> HANDLE;
type ReceivePacket = unsafe extern "system" fn(*mut std::ffi::c_void, *mut u32) -> *mut u8;
type ReleasePacket = unsafe extern "system" fn(*mut std::ffi::c_void, *const u8);
type AllocSend = unsafe extern "system" fn(*mut std::ffi::c_void, u32) -> *mut u8;
type SendPacket = unsafe extern "system" fn(*mut std::ffi::c_void, *const u8);

struct Api {
    create: CreateAdapter,
    close_adapter: CloseAdapter,
    get_luid: GetLuid,
    start: StartSession,
    end: EndSession,
    read_event: GetReadEvent,
    receive: ReceivePacket,
    release: ReleasePacket,
    alloc: AllocSend,
    send: SendPacket,
}

struct Tunnel {
    api: Api,
    adapter: *mut std::ffi::c_void,
    session: *mut std::ffi::c_void,
    read_event: HANDLE,
    plan: Plan,
    luid: u64,
    library: windows::Win32::Foundation::HMODULE,
}

unsafe impl Send for Tunnel {}
unsafe impl Sync for Tunnel {}

pub fn register_protocol() -> Result<(), String> {
    let exe = std::env::current_exe().map_err(|_| "无法定位程序自身".to_string())?;
    let command = format!("\"{}\" \"%1\"", exe.display());
    let hkcu = winreg::RegKey::predef(winreg::enums::HKEY_CURRENT_USER);
    let (key, _) = hkcu
        .create_subkey(r"Software\Classes\gamelink")
        .map_err(|_| "无法注册 gamelink:// 协议".to_string())?;
    key.set_value("", &"URL:GameLink Protocol")
        .map_err(|_| "无法注册 gamelink:// 协议".to_string())?;
    key.set_value("URL Protocol", &"")
        .map_err(|_| "无法注册 gamelink:// 协议".to_string())?;
    let (cmd, _) = hkcu
        .create_subkey(r"Software\Classes\gamelink\shell\open\command")
        .map_err(|_| "无法注册 gamelink:// 协议".to_string())?;
    cmd.set_value("", &command)
        .map_err(|_| "无法注册 gamelink:// 协议".to_string())?;
    Ok(())
}

pub fn is_elevated() -> bool {
    unsafe { IsUserAnAdmin().as_bool() }
}

pub fn proxy_tun_present() -> bool {
    conflicting_process() || fake_ip_present()
}

pub fn open_tunnel(vip: Ipv4Addr) -> Result<Arc<dyn PacketIo>, String> {
    let plan = plan::for_vip(vip)?;
    if !plan.is_safe() || plan.has_default_route() {
        return Err("拒绝安装默认路由".into());
    }
    let dll = wintun_pin::stage_beside_exe()?;
    let (library, api) = load_api(&dll)?;
    let name = wide(plan::ADAPTER_NAME);
    let kind = wide("Wintun");
    let adapter = unsafe { (api.create)(name.as_ptr(), kind.as_ptr(), std::ptr::null()) };
    if adapter.is_null() {
        let err = unsafe { GetLastError() };
        return Err(format!("创建网卡 GameLink 失败 ({})", err.0));
    }
    let mut luid = NET_LUID_LH::default();
    unsafe { (api.get_luid)(adapter, &mut luid) };
    let luid_value = unsafe { luid.Value };
    if luid_value == 0 {
        unsafe { (api.close_adapter)(adapter) };
        return Err("网卡 LUID 无效，拒绝改路由".into());
    }
    let session = unsafe { (api.start)(adapter, RING_CAPACITY) };
    if session.is_null() {
        unsafe { (api.close_adapter)(adapter) };
        return Err("启动 GameLink 会话失败".into());
    }
    let read_event = unsafe { (api.read_event)(session) };
    if let Err(err) = install_with_retry(luid_value, &plan) {
        unsafe {
            (api.end)(session);
            (api.close_adapter)(adapter);
        }
        return Err(err);
    }
    Ok(Arc::new(Tunnel {
        api,
        adapter,
        session,
        read_event,
        plan,
        luid: luid_value,
        library,
    }))
}

impl PacketIo for Tunnel {
    fn read_packet(&self, timeout: Duration) -> io::Result<Option<Vec<u8>>> {
        if let Some(pkt) = self.receive_now()? {
            return Ok(Some(pkt));
        }
        let ms = timeout.as_millis().min(u128::from(u32::MAX)) as u32;
        let waited = unsafe { WaitForSingleObject(self.read_event, ms) };
        if waited == WAIT_OBJECT_0 {
            return self.receive_now();
        }
        if waited.0 == WAIT_TIMEOUT {
            return Ok(None);
        }
        Ok(None)
    }

    fn write_packet(&self, packet: &[u8]) -> io::Result<()> {
        if packet.is_empty() || packet.len() > PACKET_MAX {
            return Ok(());
        }
        let buf = unsafe { (self.api.alloc)(self.session, packet.len() as u32) };
        if buf.is_null() {
            return Err(io::Error::other("wintun send buffer"));
        }
        unsafe {
            std::ptr::copy_nonoverlapping(packet.as_ptr(), buf, packet.len());
            (self.api.send)(self.session, buf);
        }
        Ok(())
    }
}

impl Tunnel {
    fn receive_now(&self) -> io::Result<Option<Vec<u8>>> {
        let mut size = 0u32;
        let ptr = unsafe { (self.api.receive)(self.session, &mut size) };
        if ptr.is_null() {
            let err = unsafe { GetLastError() }.0;
            if err == ERROR_NO_MORE_ITEMS || err == 0 {
                return Ok(None);
            }
            if err == ERROR_HANDLE_EOF {
                return Err(io::Error::new(io::ErrorKind::BrokenPipe, "wintun closed"));
            }
            return Err(io::Error::other(format!("wintun read {err}")));
        }
        let packet = unsafe { std::slice::from_raw_parts(ptr, size as usize) }.to_vec();
        unsafe { (self.api.release)(self.session, ptr) };
        Ok(Some(packet))
    }
}

impl Drop for Tunnel {
    fn drop(&mut self) {
        remove_config(self.luid, &self.plan);
        unsafe {
            if !self.session.is_null() {
                (self.api.end)(self.session);
                self.session = std::ptr::null_mut();
            }
            if !self.adapter.is_null() {
                (self.api.close_adapter)(self.adapter);
                self.adapter = std::ptr::null_mut();
            }
            let _keep_loaded = self.library;
        }
    }
}

fn load_api(path: &std::path::Path) -> Result<(windows::Win32::Foundation::HMODULE, Api), String> {
    let wide_path = wide(&path.display().to_string());
    let library = unsafe { LoadLibraryW(PCWSTR(wide_path.as_ptr())) }
        .map_err(|_| "找不到或无法加载 wintun.dll".to_string())?;
    unsafe {
        let api = Api {
            create: proc(library, "WintunCreateAdapter")?,
            close_adapter: proc(library, "WintunCloseAdapter")?,
            get_luid: proc(library, "WintunGetAdapterLUID")?,
            start: proc(library, "WintunStartSession")?,
            end: proc(library, "WintunEndSession")?,
            read_event: proc(library, "WintunGetReadWaitEvent")?,
            receive: proc(library, "WintunReceivePacket")?,
            release: proc(library, "WintunReleaseReceivePacket")?,
            alloc: proc(library, "WintunAllocateSendPacket")?,
            send: proc(library, "WintunSendPacket")?,
        };
        Ok((library, api))
    }
}

unsafe fn proc<T>(library: windows::Win32::Foundation::HMODULE, name: &str) -> Result<T, String> {
    let c_name = std::ffi::CString::new(name).map_err(|_| "无法加载 wintun.dll".to_string())?;
    let addr = GetProcAddress(library, PCSTR(c_name.as_ptr() as *const u8))
        .ok_or_else(|| "无法加载 wintun.dll".to_string())?;
    Ok(std::mem::transmute_copy(&addr))
}

fn install_with_retry(luid: u64, plan: &Plan) -> Result<(), String> {
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        match apply(luid, plan) {
            Ok(()) => return Ok(()),
            Err(err) if err.contains("稍后重试") && Instant::now() < deadline => {
                std::thread::sleep(Duration::from_millis(50));
            }
            Err(err) => return Err(err),
        }
    }
}

fn apply(luid: u64, plan: &Plan) -> Result<(), String> {
    if plan.has_default_route() || plan.route_len == 0 || !plan.on_link {
        return Err(format!("拒绝安装路由 {}/{}", plan.route, plan.route_len));
    }
    set_mtu(luid, plan.mtu)?;
    if let Err(code) = add_address(luid, plan.address, plan.address_len) {
        if is_transient(code) {
            return Err("设置 MTU 失败（稍后重试）".into());
        }
        if !already_exists(code) {
            return Err(format!(
                "添加地址 {}/{} 失败",
                plan.address, plan.address_len
            ));
        }
    }
    if let Err(code) = add_on_link(luid, plan.route, plan.route_len) {
        if is_transient(code) {
            return Err("设置 MTU 失败（稍后重试）".into());
        }
        if !already_exists(code) {
            let _ = delete_address(luid, plan.address);
            return Err(format!("添加路由 {}/{} 失败", plan.route, plan.route_len));
        }
    }
    Ok(())
}

fn remove_config(luid: u64, plan: &Plan) {
    let _ = delete_on_link(luid, plan.route, plan.route_len);
    let _ = delete_address(luid, plan.address);
}

fn set_mtu(luid: u64, mtu: u32) -> Result<(), String> {
    unsafe {
        let mut row = MIB_IPINTERFACE_ROW {
            Family: AF_INET,
            InterfaceLuid: net_luid(luid),
            ..std::mem::zeroed()
        };
        explain(GetIpInterfaceEntry(&mut row), "设置 MTU 失败")?;
        row.NlMtu = mtu;
        row.DisableDefaultRoutes = BOOLEAN(1);
        row.SitePrefixLength = 0;
        explain(SetIpInterfaceEntry(&mut row), "设置 MTU 失败")?;
    }
    Ok(())
}

fn add_address(luid: u64, addr: Ipv4Addr, prefix: u8) -> Result<(), u32> {
    unsafe {
        let mut row = std::mem::zeroed::<MIB_UNICASTIPADDRESS_ROW>();
        InitializeUnicastIpAddressEntry(&mut row);
        row.InterfaceLuid = net_luid(luid);
        row.OnLinkPrefixLength = prefix;
        row.DadState = IpDadStatePreferred;
        row.SkipAsSource = BOOLEAN(0);
        put_ipv4(&mut row.Address, addr);
        ok_code(CreateUnicastIpAddressEntry(&row))
    }
}

fn delete_address(luid: u64, addr: Ipv4Addr) -> Result<(), u32> {
    unsafe {
        let mut row = std::mem::zeroed::<MIB_UNICASTIPADDRESS_ROW>();
        row.InterfaceLuid = net_luid(luid);
        put_ipv4(&mut row.Address, addr);
        ok_code(DeleteUnicastIpAddressEntry(&row))
    }
}

fn add_on_link(luid: u64, prefix: Ipv4Addr, length: u8) -> Result<(), u32> {
    unsafe {
        let row = on_link_row(luid, prefix, length);
        ok_code(CreateIpForwardEntry2(&row))
    }
}

fn delete_on_link(luid: u64, prefix: Ipv4Addr, length: u8) -> Result<(), u32> {
    unsafe {
        let row = on_link_row(luid, prefix, length);
        ok_code(DeleteIpForwardEntry2(&row))
    }
}

fn ok_code(code: WIN32_ERROR) -> Result<(), u32> {
    if code.0 == 0 {
        Ok(())
    } else {
        Err(code.0)
    }
}

fn explain(code: WIN32_ERROR, what: &str) -> Result<(), String> {
    if code.0 == 0 {
        Ok(())
    } else if is_transient(code.0) {
        Err(format!("{what}（稍后重试）"))
    } else {
        Err(what.into())
    }
}

unsafe fn on_link_row(luid: u64, prefix: Ipv4Addr, length: u8) -> MIB_IPFORWARD_ROW2 {
    let mut row = std::mem::zeroed::<MIB_IPFORWARD_ROW2>();
    InitializeIpForwardEntry(&mut row);
    row.InterfaceLuid = net_luid(luid);
    put_ipv4(&mut row.DestinationPrefix.Prefix, prefix);
    row.DestinationPrefix.PrefixLength = length;
    row.NextHop.si_family = AF_INET;
    row.Metric = 1;
    row
}

fn net_luid(value: u64) -> NET_LUID_LH {
    NET_LUID_LH { Value: value }
}

unsafe fn put_ipv4(dest: *mut SOCKADDR_INET, ip: Ipv4Addr) {
    let dest = &mut *dest;
    dest.si_family = AF_INET;
    dest.Ipv4.sin_family = AF_INET;
    dest.Ipv4.sin_port = 0;
    dest.Ipv4.sin_addr.S_un.S_addr = u32::from_ne_bytes(ip.octets());
}

fn already_exists(code: u32) -> bool {
    code == ERROR_OBJECT_ALREADY_EXISTS.0 || code == ERROR_ALREADY_EXISTS.0
}

fn is_transient(code: u32) -> bool {
    code == ERROR_NOT_FOUND.0 || code == ERROR_FILE_NOT_FOUND.0 || code == ERROR_NO_SUCH_DEVICE.0
}

fn conflicting_process() -> bool {
    unsafe {
        let snapshot = match CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0) {
            Ok(handle) => handle,
            Err(_) => return false,
        };
        let mut entry = PROCESSENTRY32W {
            dwSize: std::mem::size_of::<PROCESSENTRY32W>() as u32,
            ..std::mem::zeroed()
        };
        let mut found = false;
        if Process32FirstW(snapshot, &mut entry).is_ok() {
            loop {
                let name = utf16_name(&entry.szExeFile);
                if proxy::is_proxy_process(&name) {
                    found = true;
                    break;
                }
                if Process32NextW(snapshot, &mut entry).is_err() {
                    break;
                }
            }
        }
        let _ = CloseHandle(snapshot);
        found
    }
}

fn fake_ip_present() -> bool {
    contains_fake_ip(&adapter_ipv4s())
}

fn adapter_ipv4s() -> Vec<Ipv4Addr> {
    let mut found = Vec::new();
    unsafe {
        let mut size = 0u32;
        let flags = GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST | GAA_FLAG_SKIP_DNS_SERVER;
        let family = AF_UNSPEC.0 as u32;
        let _ = GetAdaptersAddresses(family, flags, None, None, &mut size);
        if size == 0 {
            return found;
        }
        let mut buf = vec![0u8; size as usize + 512];
        let ptr = buf.as_mut_ptr() as *mut IP_ADAPTER_ADDRESSES_LH;
        if GetAdaptersAddresses(family, flags, None, Some(ptr), &mut size) != 0 {
            return found;
        }
        let mut cursor = ptr;
        while !cursor.is_null() {
            let adapter = &*cursor;
            let mut unicast = adapter.FirstUnicastAddress;
            while !unicast.is_null() {
                let address = &*unicast;
                if !address.Address.lpSockaddr.is_null()
                    && (*address.Address.lpSockaddr).sa_family == AF_INET
                {
                    let v4 = &*(address.Address.lpSockaddr as *const SOCKADDR_IN);
                    let raw = v4.sin_addr.S_un.S_addr;
                    found.push(Ipv4Addr::from(u32::from_be_bytes(raw.to_ne_bytes())));
                }
                unicast = address.Next;
            }
            cursor = adapter.Next;
        }
    }
    found
}

fn utf16_name(buf: &[u16]) -> String {
    let end = buf.iter().position(|c| *c == 0).unwrap_or(buf.len());
    String::from_utf16_lossy(&buf[..end])
}

fn wide(text: &str) -> Vec<u16> {
    text.encode_utf16().chain(std::iter::once(0)).collect()
}

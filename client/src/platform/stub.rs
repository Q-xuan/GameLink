use std::net::Ipv4Addr;
use std::sync::Arc;

use crate::device::PacketIo;

pub fn register_protocol() -> Result<(), String> {
    Ok(())
}

pub fn is_elevated() -> bool {
    true
}

pub fn proxy_tun_present() -> bool {
    false
}

pub fn open_tunnel(_vip: Ipv4Addr) -> Result<Arc<dyn PacketIo>, String> {
    Err("GameLink 网卡只能在 Windows 上创建".into())
}

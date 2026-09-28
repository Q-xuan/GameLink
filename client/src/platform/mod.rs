//! Operating-system pieces. The Wintun adapter exists only on Windows.

use std::net::Ipv4Addr;
use std::sync::Arc;

use crate::device::PacketIo;

#[cfg(windows)]
#[path = "windows.rs"]
mod imp;

#[cfg(not(windows))]
#[path = "stub.rs"]
mod imp;

pub fn register_protocol() -> Result<(), String> {
    imp::register_protocol()
}

pub fn is_elevated() -> bool {
    imp::is_elevated()
}

pub fn proxy_tun_present() -> bool {
    imp::proxy_tun_present()
}

pub fn open_tunnel(vip: Ipv4Addr) -> Result<Arc<dyn PacketIo>, String> {
    imp::open_tunnel(vip)
}

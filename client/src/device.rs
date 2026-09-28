//! Packet source used by the forwarder. Windows supplies a Wintun adapter.

use std::io;
use std::time::Duration;

pub trait PacketIo: Send + Sync {
    fn read_packet(&self, timeout: Duration) -> io::Result<Option<Vec<u8>>>;
    fn write_packet(&self, packet: &[u8]) -> io::Result<()>;
}

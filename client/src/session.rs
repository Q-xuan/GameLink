//! One UDP association with the relay. MAC is always required.
//! The socket is not bound to the control-plane source address.

use std::io;
use std::net::{SocketAddr, ToSocketAddrs, UdpSocket};
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};
use std::time::{Duration, Instant};

use crate::frame::{self, FrameError};
use crate::protocol::{self, Header, TYPE_HANDSHAKE};
use crate::proxy::HANDSHAKE_DROPPED;

pub const PING_EVERY: Duration = Duration::from_secs(5);
pub const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(15);

#[derive(Debug)]
pub enum SessionError {
    Address,
    Io(io::Error),
    Frame(FrameError),
    HandshakeTimeout,
    Timeout,
}

impl std::fmt::Display for SessionError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            SessionError::Address => write!(f, "中继地址无效"),
            SessionError::Io(_) => write!(f, "连接中继失败"),
            SessionError::Frame(FrameError::BadMac) => write!(f, "校验失败"),
            SessionError::Frame(FrameError::MacRequired) => write!(f, "缺少校验"),
            SessionError::Frame(_) => write!(f, "数据包无效"),
            SessionError::HandshakeTimeout => write!(f, "{HANDSHAKE_DROPPED}"),
            SessionError::Timeout => write!(f, "等待超时"),
        }
    }
}

pub struct UdpSession {
    socket: UdpSocket,
    room_id: u64,
    peer_id: u32,
    token: [u8; 16],
    seq: AtomicU32,
}

impl UdpSession {
    /// `local` is optional. The GUI passes `None`, so the kernel chooses the
    /// UDP source. That address may differ from the HTTP client source.
    pub fn dial(
        relay: &str,
        local: Option<&str>,
        room_id: u64,
        peer_id: u32,
        token: [u8; 16],
    ) -> Result<Self, SessionError> {
        let remote = resolve_one(relay)?;
        let socket = match local {
            Some(local) => {
                let addr = resolve_one(local)?;
                UdpSocket::bind(addr).map_err(SessionError::Io)?
            }
            None => {
                let bind_to = if remote.is_ipv4() {
                    "0.0.0.0:0"
                } else {
                    "[::]:0"
                };
                UdpSocket::bind(bind_to).map_err(SessionError::Io)?
            }
        };
        socket.connect(remote).map_err(SessionError::Io)?;
        Ok(Self {
            socket,
            room_id,
            peer_id,
            token,
            seq: AtomicU32::new(0),
        })
    }

    pub fn local_addr(&self) -> io::Result<SocketAddr> {
        self.socket.local_addr()
    }

    pub fn peer_id(&self) -> u32 {
        self.peer_id
    }

    pub fn room_id(&self) -> u64 {
        self.room_id
    }

    /// Sequence starts at 1, matching the Go atomic add.
    pub fn send(&self, typ: u8, dst: u32, payload: &[u8]) -> Result<u32, SessionError> {
        let sequence = self.seq.fetch_add(1, Ordering::Relaxed).wrapping_add(1);
        let header = Header {
            version: protocol::VERSION,
            typ,
            flags: 0,
            room_id: self.room_id,
            src_peer: self.peer_id,
            dst_peer: dst,
            sequence,
            payload_len: 0,
            reserved: 0,
        };
        let buf = frame::seal(self.token, header, payload).map_err(SessionError::Frame)?;
        self.socket.send(&buf).map_err(SessionError::Io)?;
        Ok(sequence)
    }

    pub fn handshake(&self, timeout: Duration, stop: &AtomicBool) -> Result<(), SessionError> {
        let deadline = Instant::now() + timeout;
        let mut next_send = Instant::now();
        let mut buf = [0u8; 2048];
        while Instant::now() < deadline {
            if stop.load(Ordering::Relaxed) {
                return Err(SessionError::Timeout);
            }
            if Instant::now() >= next_send {
                self.send(TYPE_HANDSHAKE, 0, &self.token)?;
                next_send = Instant::now() + Duration::from_millis(200);
            }
            let wait = next_send
                .saturating_duration_since(Instant::now())
                .min(deadline.saturating_duration_since(Instant::now()));
            if !self.recv_one(&mut buf, wait, true)? {
                continue;
            }
            return Ok(());
        }
        Err(SessionError::HandshakeTimeout)
    }

    /// Read one authenticated datagram for this peer, or time out.
    pub fn recv_deadline(&self, timeout: Duration) -> Result<(Header, Vec<u8>), SessionError> {
        let deadline = Instant::now() + timeout;
        let mut buf = [0u8; 65535];
        while Instant::now() < deadline {
            let wait = (deadline.saturating_duration_since(Instant::now()))
                .min(Duration::from_millis(200))
                .max(Duration::from_millis(1));
            self.socket
                .set_read_timeout(Some(wait))
                .map_err(SessionError::Io)?;
            match self.socket.recv(&mut buf) {
                Ok(n) => match frame::authenticate(self.token, &buf[..n], true) {
                    Ok((h, payload)) if h.room_id == self.room_id && h.dst_peer == self.peer_id => {
                        return Ok((h, payload));
                    }
                    _ => continue,
                },
                Err(err) if is_timeout(&err) => continue,
                Err(err) => return Err(SessionError::Io(err)),
            }
        }
        Err(SessionError::Timeout)
    }

    fn recv_one(
        &self,
        buf: &mut [u8],
        wait: Duration,
        want_handshake: bool,
    ) -> Result<bool, SessionError> {
        let wait = wait.max(Duration::from_millis(1));
        self.socket
            .set_read_timeout(Some(wait))
            .map_err(SessionError::Io)?;
        match self.socket.recv(buf) {
            Ok(n) => match frame::authenticate(self.token, &buf[..n], true) {
                Ok((h, _))
                    if h.room_id == self.room_id
                        && h.dst_peer == self.peer_id
                        && (!want_handshake || h.typ == TYPE_HANDSHAKE) =>
                {
                    Ok(true)
                }
                _ => Ok(false),
            },
            Err(err) if is_timeout(&err) => Ok(false),
            Err(_) => Ok(false),
        }
    }
}

fn resolve_one(addr: &str) -> Result<SocketAddr, SessionError> {
    addr.to_socket_addrs()
        .map_err(|_| SessionError::Address)?
        .find(|a| a.is_ipv4())
        .or_else(|| addr.to_socket_addrs().ok().and_then(|mut it| it.next()))
        .ok_or(SessionError::Address)
}

fn is_timeout(err: &io::Error) -> bool {
    err.kind() == io::ErrorKind::TimedOut || err.kind() == io::ErrorKind::WouldBlock
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::frame::seal;
    use crate::ipv4::{self, check_inner_ipv4};
    use crate::protocol::*;
    use std::collections::HashMap;
    use std::net::Ipv4Addr;
    use std::sync::Arc;
    use std::thread;

    #[test]
    fn timing_constants() {
        assert_eq!(PING_EVERY, Duration::from_secs(5));
        assert!(HANDSHAKE_TIMEOUT >= Duration::from_secs(5));
        assert!(HANDSHAKE_TIMEOUT <= Duration::from_secs(30));
    }

    #[test]
    fn dial_without_local_leaves_the_source_to_the_kernel() {
        let session = UdpSession::dial("127.0.0.1:9", None, 1, 1, [1u8; 16]).unwrap();
        let addr = session.local_addr().unwrap();
        assert_ne!(addr.port(), 0);
    }

    #[test]
    fn handshake_timeout_is_bounded() {
        let parked = UdpSocket::bind("127.0.0.1:0").unwrap();
        let session = UdpSession::dial(
            &parked.local_addr().unwrap().to_string(),
            None,
            1,
            1,
            [9u8; 16],
        )
        .unwrap();
        let err = session
            .handshake(Duration::from_millis(350), &AtomicBool::new(false))
            .unwrap_err();
        assert!(matches!(err, SessionError::HandshakeTimeout));
        assert_eq!(err.to_string(), HANDSHAKE_DROPPED);
    }

    #[test]
    fn handshake_ping_and_unicast_only() {
        let token = *b"0123456789abcdef";
        let room = 42u64;
        let relay = UdpSocket::bind("127.0.0.1:0").unwrap();
        let relay_addr = relay.local_addr().unwrap();
        let stop = Arc::new(AtomicBool::new(false));
        let stop_relay = stop.clone();
        let relay_thread = thread::spawn(move || mock_relay(relay, token, room, stop_relay));

        let s1 = UdpSession::dial(&relay_addr.to_string(), None, room, 1, token).unwrap();
        let s2 = UdpSession::dial(&relay_addr.to_string(), None, room, 2, token).unwrap();
        s1.handshake(Duration::from_secs(2), &AtomicBool::new(false))
            .unwrap();
        s2.handshake(Duration::from_secs(2), &AtomicBool::new(false))
            .unwrap();

        let seq = s1.send(TYPE_PING, 0, b"").unwrap();
        let (h, payload) = s1.recv_deadline(Duration::from_secs(2)).unwrap();
        assert_eq!(h.typ, TYPE_PONG);
        assert_eq!(h.sequence, seq);
        assert!(payload.is_empty());

        let pkt = ipv4::sample_packet(
            17,
            Ipv4Addr::new(10, 66, 0, 1),
            Ipv4Addr::new(10, 66, 0, 2),
            b"ok",
        );
        s1.send(TYPE_DATA, 2, &pkt).unwrap();
        let (h, got) = s2.recv_deadline(Duration::from_secs(2)).unwrap();
        assert_eq!(h.typ, TYPE_DATA);
        assert_eq!(got, pkt);

        let bad = ipv4::sample_packet(1, Ipv4Addr::new(10, 66, 0, 1), Ipv4Addr::BROADCAST, b"");
        s1.send(TYPE_DATA, 2, &bad).unwrap();
        assert!(s2.recv_deadline(Duration::from_millis(250)).is_err());

        stop.store(true, Ordering::Relaxed);
        let _ = s1.send(TYPE_PING, 0, b"");
        relay_thread.join().unwrap();
    }

    fn mock_relay(sock: UdpSocket, token: [u8; 16], room: u64, stop: Arc<AtomicBool>) {
        sock.set_read_timeout(Some(Duration::from_millis(100))).ok();
        let mut peers: HashMap<u32, SocketAddr> = HashMap::new();
        let mut buf = [0u8; 65535];
        while !stop.load(Ordering::Relaxed) {
            let (n, src) = match sock.recv_from(&mut buf) {
                Ok(v) => v,
                Err(_) => continue,
            };
            let (h, payload) = match frame::authenticate(token, &buf[..n], true) {
                Ok(v) => v,
                Err(_) => continue,
            };
            if h.room_id != room {
                continue;
            }
            match h.typ {
                TYPE_HANDSHAKE => {
                    if payload.as_slice() != token {
                        continue;
                    }
                    peers.insert(h.src_peer, src);
                    reply(&sock, src, token, &h, TYPE_HANDSHAKE, b"");
                }
                TYPE_PING => {
                    if !peers.contains_key(&h.src_peer) {
                        continue;
                    }
                    peers.insert(h.src_peer, src);
                    reply(&sock, src, token, &h, TYPE_PONG, b"");
                }
                TYPE_DATA => {
                    if !peers.contains_key(&h.src_peer) {
                        continue;
                    }
                    let Some(vip) = ipv4::virtual_ip(h.src_peer) else {
                        continue;
                    };
                    if !check_inner_ipv4(&payload, vip) {
                        continue;
                    }
                    if h.dst_peer == 0 || h.dst_peer == h.src_peer {
                        continue;
                    }
                    if let Some(dst) = peers.get(&h.dst_peer) {
                        let _ = sock.send_to(&buf[..n], dst);
                    }
                }
                _ => {}
            }
        }
    }

    fn reply(
        sock: &UdpSocket,
        dst: SocketAddr,
        token: [u8; 16],
        incoming: &Header,
        typ: u8,
        payload: &[u8],
    ) {
        let header = Header {
            version: VERSION,
            typ,
            flags: 0,
            room_id: incoming.room_id,
            src_peer: 0,
            dst_peer: incoming.src_peer,
            sequence: incoming.sequence,
            payload_len: 0,
            reserved: 0,
        };
        if let Ok(buf) = seal(token, header, payload) {
            let _ = sock.send_to(&buf, dst);
        }
    }
}

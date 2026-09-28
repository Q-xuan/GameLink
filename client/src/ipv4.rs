//! Unicast IPv4 checks copied from the Go relay and TUN forwarder.
//! Ports are not inspected.

use std::net::Ipv4Addr;

pub const MAX_PEERS: u32 = 8;

pub fn virtual_ip(id: u32) -> Option<Ipv4Addr> {
    if (1..=MAX_PEERS).contains(&id) {
        Some(Ipv4Addr::new(10, 66, 0, id as u8))
    } else {
        None
    }
}

pub fn is_broadcast(dst: Ipv4Addr) -> bool {
    dst == Ipv4Addr::BROADCAST || dst == Ipv4Addr::new(10, 66, 0, 255)
}

/// Accept a unicast IPv4 payload whose source equals `vip`.
pub fn check_inner_ipv4(payload: &[u8], vip: Ipv4Addr) -> bool {
    let Some((src, dst)) = ipv4_ends(payload) else {
        return false;
    };
    src == vip && !dst.is_multicast() && !is_broadcast(dst)
}

/// Map an outgoing unicast IPv4 packet onto a room peer.
/// Non-IPv4, broadcast, multicast, self, and addresses outside 10.66.0.1–8 are dropped.
pub fn dest_peer(pkt: &[u8], local: Ipv4Addr) -> Option<u32> {
    let dst = ipv4_unicast_dest(pkt)?;
    if dst == local {
        return None;
    }
    peer_id(dst)
}

/// Whether an inbound packet may be written to the adapter.
pub fn deliverable(pkt: &[u8]) -> bool {
    ipv4_unicast_dest(pkt).is_some()
}

pub fn peer_id(dst: Ipv4Addr) -> Option<u32> {
    let o = dst.octets();
    if o[0] != 10 || o[1] != 66 || o[2] != 0 {
        return None;
    }
    let id = u32::from(o[3]);
    match virtual_ip(id) {
        Some(vip) if vip == dst => Some(id),
        _ => None,
    }
}

fn ipv4_unicast_dest(pkt: &[u8]) -> Option<Ipv4Addr> {
    let (_src, dst) = ipv4_ends(pkt)?;
    if !dst.is_unspecified() && !dst.is_multicast() && !is_broadcast(dst) {
        Some(dst)
    } else {
        None
    }
}

fn ipv4_ends(pkt: &[u8]) -> Option<(Ipv4Addr, Ipv4Addr)> {
    if pkt.len() < 20 || pkt[0] >> 4 != 4 {
        return None;
    }
    let ihl = (pkt[0] & 0x0f) as usize * 4;
    if ihl < 20 || pkt.len() < ihl {
        return None;
    }
    let src = Ipv4Addr::new(pkt[12], pkt[13], pkt[14], pkt[15]);
    let dst = Ipv4Addr::new(pkt[16], pkt[17], pkt[18], pkt[19]);
    Some((src, dst))
}

pub fn sample_packet(proto: u8, src: Ipv4Addr, dst: Ipv4Addr, payload: &[u8]) -> Vec<u8> {
    let mut b = vec![0u8; 20 + payload.len()];
    b[0] = 0x45;
    let len = b.len() as u16;
    b[2..4].copy_from_slice(&len.to_be_bytes());
    b[8] = 64;
    b[9] = proto;
    b[12..16].copy_from_slice(&src.octets());
    b[16..20].copy_from_slice(&dst.octets());
    b[20..].copy_from_slice(payload);
    b
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn virtual_ip_matches_go() {
        assert_eq!(virtual_ip(1), Some(Ipv4Addr::new(10, 66, 0, 1)));
        assert_eq!(virtual_ip(8), Some(Ipv4Addr::new(10, 66, 0, 8)));
        assert_eq!(virtual_ip(0), None);
        assert_eq!(virtual_ip(9), None);
    }

    #[test]
    fn drop_non_unicast_and_foreign_source() {
        let vip = Ipv4Addr::new(10, 66, 0, 1);
        let drops: Vec<Vec<u8>> = vec![
            vec![
                0x60, 0, 0, 20, 0, 0, 0, 0, 64, 17, 0, 0, 10, 66, 0, 1, 10, 66, 0, 2,
            ],
            sample_packet(1, vip, Ipv4Addr::BROADCAST, b""),
            sample_packet(1, vip, Ipv4Addr::new(10, 66, 0, 255), b""),
            sample_packet(1, vip, Ipv4Addr::new(224, 0, 0, 1), b""),
            sample_packet(
                17,
                Ipv4Addr::new(10, 66, 0, 9),
                Ipv4Addr::new(10, 66, 0, 2),
                b"",
            ),
            vec![0x45],
        ];
        for p in &drops {
            assert!(!check_inner_ipv4(p, vip), "{p:?}");
        }
        let good = sample_packet(17, vip, Ipv4Addr::new(10, 66, 0, 2), &[7]);
        assert!(check_inner_ipv4(&good, vip));
    }

    #[test]
    fn dest_peer_drops_match_forwarder() {
        let local = Ipv4Addr::new(10, 66, 0, 1);
        let keep = sample_packet(17, local, Ipv4Addr::new(10, 66, 0, 2), b"ok");
        assert_eq!(dest_peer(&keep, local), Some(2));
        let drops = [
            sample_packet(1, local, Ipv4Addr::BROADCAST, b""),
            sample_packet(1, local, Ipv4Addr::new(10, 66, 0, 255), b""),
            sample_packet(17, local, Ipv4Addr::new(224, 0, 0, 1), &[1]),
            sample_packet(17, local, Ipv4Addr::new(10, 66, 0, 9), &[1]),
            sample_packet(1, local, local, &[1]),
            vec![0x60, 0, 0, 0, 0, 8, 58, 64],
        ];
        for pkt in drops {
            assert_eq!(dest_peer(&pkt, local), None);
        }
        assert!(deliverable(&keep));
        assert!(!deliverable(&[0x60, 0, 0, 0]));
    }
}

//! 32-byte GameLink UDP header. Multi-byte integers are big-endian.
//!
//! ```text
//! 0  magic "GLNK" u32
//! 4  version u8 = 1
//! 5  type u8   Data=1 Ping=2 Pong=3 Handshake=4
//! 6  flags u16 bit0 = 16-byte truncated HMAC-SHA256 follows payload
//! 8  room_id u64
//! 16 src_peer u32
//! 20 dst_peer u32
//! 24 sequence u32
//! 28 payload_len u16
//! 30 reserved u16
//! ```
//!
//! `payload_len` covers the payload only. The MAC is not included in it.
//! Sequence is statistics only.

pub const MAGIC: &[u8; 4] = b"GLNK";
pub const VERSION: u8 = 1;
pub const HEADER_SIZE: usize = 32;
pub const MAC_SIZE: usize = 16;
pub const TOKEN_SIZE: usize = 16;

pub const TYPE_DATA: u8 = 1;
pub const TYPE_PING: u8 = 2;
pub const TYPE_PONG: u8 = 3;
pub const TYPE_HANDSHAKE: u8 = 4;

/// Bit0. When set, a truncated HMAC-SHA256 follows the payload.
pub const FLAG_MAC: u16 = 1 << 0;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Header {
    pub version: u8,
    pub typ: u8,
    pub flags: u16,
    pub room_id: u64,
    pub src_peer: u32,
    pub dst_peer: u32,
    pub sequence: u32,
    pub payload_len: u16,
    pub reserved: u16,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ProtoError {
    Short,
    Magic,
    Version,
    Type,
    Payload,
    Trailing,
}

pub fn mac_present(flags: u16) -> bool {
    flags & FLAG_MAC != 0
}

/// Encode `h` and `payload`. `payload_len` is taken from `payload.len()`.
pub fn marshal(mut h: Header, payload: &[u8]) -> Result<Vec<u8>, ProtoError> {
    if h.version != VERSION {
        return Err(ProtoError::Version);
    }
    if h.typ == 0 {
        return Err(ProtoError::Type);
    }
    if payload.len() > u16::MAX as usize {
        return Err(ProtoError::Payload);
    }
    h.payload_len = payload.len() as u16;
    let mut buf = vec![0u8; HEADER_SIZE + payload.len()];
    buf[0..4].copy_from_slice(MAGIC);
    buf[4] = h.version;
    buf[5] = h.typ;
    buf[6..8].copy_from_slice(&h.flags.to_be_bytes());
    buf[8..16].copy_from_slice(&h.room_id.to_be_bytes());
    buf[16..20].copy_from_slice(&h.src_peer.to_be_bytes());
    buf[20..24].copy_from_slice(&h.dst_peer.to_be_bytes());
    buf[24..28].copy_from_slice(&h.sequence.to_be_bytes());
    buf[28..30].copy_from_slice(&h.payload_len.to_be_bytes());
    buf[30..32].copy_from_slice(&h.reserved.to_be_bytes());
    buf[HEADER_SIZE..].copy_from_slice(payload);
    Ok(buf)
}

/// Split a datagram into header, payload, and optional MAC.
/// The returned slices alias `b`.
pub fn decode(b: &[u8]) -> Result<(Header, &[u8], Option<&[u8]>), ProtoError> {
    if b.len() < HEADER_SIZE {
        return Err(ProtoError::Short);
    }
    if &b[0..4] != MAGIC {
        return Err(ProtoError::Magic);
    }
    let h = Header {
        version: b[4],
        typ: b[5],
        flags: u16::from_be_bytes([b[6], b[7]]),
        room_id: u64::from_be_bytes(b[8..16].try_into().unwrap()),
        src_peer: u32::from_be_bytes(b[16..20].try_into().unwrap()),
        dst_peer: u32::from_be_bytes(b[20..24].try_into().unwrap()),
        sequence: u32::from_be_bytes(b[24..28].try_into().unwrap()),
        payload_len: u16::from_be_bytes([b[28], b[29]]),
        reserved: u16::from_be_bytes([b[30], b[31]]),
    };
    if h.version != VERSION {
        return Err(ProtoError::Version);
    }
    let need = HEADER_SIZE + h.payload_len as usize;
    let mac_n = if mac_present(h.flags) { MAC_SIZE } else { 0 };
    if b.len() < need + mac_n {
        return Err(ProtoError::Short);
    }
    if b.len() != need + mac_n {
        return Err(ProtoError::Trailing);
    }
    let payload = &b[HEADER_SIZE..need];
    let mac = if mac_n > 0 {
        Some(&b[need..need + mac_n])
    } else {
        None
    };
    Ok((h, payload, mac))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn marshal_golden_big_endian() {
        let h = Header {
            version: VERSION,
            typ: TYPE_DATA,
            flags: FLAG_MAC,
            room_id: 0x2a,
            src_peer: 1,
            dst_peer: 2,
            sequence: 9,
            payload_len: 0,
            reserved: 0,
        };
        let got = marshal(h, b"test").unwrap();
        let want = [
            b'G', b'L', b'N', b'K', 0x01, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
            0x00, 0x2a, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x09,
            0x00, 0x04, 0x00, 0x00, b't', b'e', b's', b't',
        ];
        assert_eq!(got, want);
        assert_eq!(u16::from_be_bytes([got[6], got[7]]), FLAG_MAC);
        assert!(mac_present(FLAG_MAC));
        assert!(!mac_present(0));
    }

    #[test]
    fn round_trip() {
        let cases = [
            Header {
                version: VERSION,
                typ: TYPE_DATA,
                flags: 0,
                room_id: 1,
                src_peer: 1,
                dst_peer: 2,
                sequence: 7,
                payload_len: 0,
                reserved: 0,
            },
            Header {
                version: VERSION,
                typ: TYPE_PING,
                flags: FLAG_MAC,
                room_id: u64::MAX,
                src_peer: 8,
                dst_peer: 0,
                sequence: 1 << 31,
                payload_len: 0,
                reserved: 0xabcd,
            },
            Header {
                version: VERSION,
                typ: TYPE_PONG,
                flags: 0,
                room_id: 99,
                src_peer: 0,
                dst_peer: 3,
                payload_len: 0,
                sequence: 0,
                reserved: 0,
            },
            Header {
                version: VERSION,
                typ: TYPE_HANDSHAKE,
                flags: FLAG_MAC | 0x0002,
                room_id: 5,
                src_peer: 2,
                dst_peer: 0,
                sequence: 0,
                payload_len: 0,
                reserved: 0,
            },
        ];
        let payloads: [&[u8]; 3] = [b"", b"x", &[0xab; 1200]];
        for h in cases {
            for p in payloads {
                let mut buf = marshal(h, p).unwrap();
                if h.flags & FLAG_MAC != 0 {
                    buf.extend(std::iter::repeat(0x11).take(MAC_SIZE));
                }
                let (got, payload, mac) = decode(&buf).unwrap();
                let mut want = h;
                want.payload_len = p.len() as u16;
                assert_eq!(got, want);
                assert_eq!(payload, p);
                if mac_present(h.flags) {
                    assert_eq!(mac.map(|m| m.len()), Some(MAC_SIZE));
                } else {
                    assert!(mac.is_none());
                }
            }
        }
    }

    #[test]
    fn decode_errors() {
        let good = marshal(
            Header {
                version: VERSION,
                typ: TYPE_PING,
                flags: 0,
                room_id: 1,
                src_peer: 1,
                dst_peer: 0,
                sequence: 0,
                payload_len: 0,
                reserved: 0,
            },
            b"",
        )
        .unwrap();
        assert_eq!(decode(&good[..10]).unwrap_err(), ProtoError::Short);
        let mut bad_magic = good.clone();
        bad_magic[0] = b'X';
        assert_eq!(decode(&bad_magic).unwrap_err(), ProtoError::Magic);
        let mut bad_ver = good.clone();
        bad_ver[4] = 2;
        assert_eq!(decode(&bad_ver).unwrap_err(), ProtoError::Version);
        let mut trailing = good.clone();
        trailing.push(0xff);
        assert_eq!(decode(&trailing).unwrap_err(), ProtoError::Trailing);
        let mut with_mac = good.clone();
        with_mac[6..8].copy_from_slice(&FLAG_MAC.to_be_bytes());
        assert_eq!(decode(&with_mac).unwrap_err(), ProtoError::Short);
        assert_eq!(
            marshal(
                Header {
                    version: 2,
                    typ: TYPE_PING,
                    ..Header {
                        version: VERSION,
                        typ: TYPE_PING,
                        flags: 0,
                        room_id: 0,
                        src_peer: 0,
                        dst_peer: 0,
                        sequence: 0,
                        payload_len: 0,
                        reserved: 0,
                    }
                },
                b"",
            )
            .unwrap_err(),
            ProtoError::Version
        );
        assert_eq!(
            marshal(
                Header {
                    version: VERSION,
                    typ: 0,
                    flags: 0,
                    room_id: 0,
                    src_peer: 0,
                    dst_peer: 0,
                    sequence: 0,
                    payload_len: 0,
                    reserved: 0,
                },
                b"",
            )
            .unwrap_err(),
            ProtoError::Type
        );
    }

    #[test]
    fn payload_len_excludes_mac() {
        let h = Header {
            version: VERSION,
            typ: TYPE_DATA,
            flags: FLAG_MAC,
            room_id: 3,
            src_peer: 1,
            dst_peer: 2,
            sequence: 4,
            payload_len: 0,
            reserved: 0,
        };
        let payload = [1u8, 2, 3, 4, 5];
        let mut buf = marshal(h, &payload).unwrap();
        assert_eq!(u16::from_be_bytes([buf[28], buf[29]]), payload.len() as u16);
        buf.extend_from_slice(&[0u8; MAC_SIZE]);
        let (got, p, mac) = decode(&buf).unwrap();
        assert_eq!(got.payload_len as usize, payload.len());
        assert_eq!(p.len(), payload.len());
        assert_eq!(mac.unwrap().len(), MAC_SIZE);
        assert_eq!(buf.len(), HEADER_SIZE + payload.len() + MAC_SIZE);
    }
}

//! Seal and check the room MAC. The client always requires a MAC.

use crate::mac;
use crate::protocol::{self, Header, FLAG_MAC};

#[derive(Debug, PartialEq, Eq)]
pub enum FrameError {
    Proto(protocol::ProtoError),
    BadMac,
    MacRequired,
}

/// Append a 16-byte truncated HMAC over the header and payload.
pub fn seal(token: [u8; 16], mut h: Header, payload: &[u8]) -> Result<Vec<u8>, FrameError> {
    h.flags |= FLAG_MAC;
    let mut buf = protocol::marshal(h, payload).map_err(FrameError::Proto)?;
    let mac = mac::sum(&token, &buf);
    buf.extend_from_slice(&mac);
    Ok(buf)
}

/// Check the MAC policy and return the header and an owned payload.
pub fn authenticate(
    token: [u8; 16],
    buf: &[u8],
    require_mac: bool,
) -> Result<(Header, Vec<u8>), FrameError> {
    let (h, payload, mac) = protocol::decode(buf).map_err(FrameError::Proto)?;
    if protocol::mac_present(h.flags) {
        let covered = &buf[..protocol::HEADER_SIZE + payload.len()];
        let tag = mac.ok_or(FrameError::BadMac)?;
        if !mac::verify(&token, covered, tag) {
            return Err(FrameError::BadMac);
        }
    } else if require_mac {
        return Err(FrameError::MacRequired);
    }
    Ok((h, payload.to_vec()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol::*;

    fn header() -> Header {
        Header {
            version: VERSION,
            typ: TYPE_DATA,
            flags: FLAG_MAC,
            room_id: 0x2a,
            src_peer: 1,
            dst_peer: 2,
            sequence: 9,
            payload_len: 0,
            reserved: 0,
        }
    }

    #[test]
    fn seal_matches_go_golden_vector() {
        let token = *b"0123456789abcdef";
        let got = seal(token, header(), b"test").unwrap();
        let want = hex_decode(
            "474c4e4b01010001000000000000002a0000000100000002000000090004000074657374\
             1ea8db522c8b7a3f79554e0aff5db7a9",
        );
        assert_eq!(got, want);
        let (h, payload) = authenticate(token, &got, true).unwrap();
        assert_eq!(h.room_id, 0x2a);
        assert_eq!(h.sequence, 9);
        assert_eq!(payload, b"test");
        assert!(authenticate(token, &got, true).is_ok());
        let mut bad = got.clone();
        let last = bad.len() - 1;
        bad[last] ^= 0x5a;
        assert_eq!(
            authenticate(token, &bad, true).unwrap_err(),
            FrameError::BadMac
        );
    }

    #[test]
    fn missing_mac_is_rejected_when_required() {
        let token = [7u8; 16];
        let buf = marshal(
            Header {
                version: VERSION,
                typ: TYPE_PING,
                flags: 0,
                room_id: 1,
                src_peer: 1,
                dst_peer: 0,
                sequence: 1,
                payload_len: 0,
                reserved: 0,
            },
            b"",
        )
        .unwrap();
        assert_eq!(
            authenticate(token, &buf, true).unwrap_err(),
            FrameError::MacRequired
        );
        assert!(authenticate(token, &buf, false).is_ok());
    }

    fn hex_decode(s: &str) -> Vec<u8> {
        (0..s.len())
            .step_by(2)
            .map(|i| u8::from_str_radix(&s[i..i + 2], 16).unwrap())
            .collect()
    }
}

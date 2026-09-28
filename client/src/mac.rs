//! Room-token HMAC. 16-byte truncated HMAC-SHA256.
//! The key is the 128-bit room token. The MAC covers the header and payload,
//! and does not cover itself.

use hmac::{Hmac, Mac};
use sha2::Sha256;

pub const SIZE: usize = 16;

type HmacSha256 = Hmac<Sha256>;

pub fn sum(key: &[u8], msg: &[u8]) -> [u8; SIZE] {
    let mut mac = HmacSha256::new_from_slice(key).expect("hmac accepts any key length");
    mac.update(msg);
    let full = mac.finalize().into_bytes();
    let mut out = [0u8; SIZE];
    out.copy_from_slice(&full[..SIZE]);
    out
}

pub fn verify(key: &[u8], msg: &[u8], mac: &[u8]) -> bool {
    if mac.len() != SIZE {
        return false;
    }
    let got = sum(key, msg);
    // Compare in constant time for the fixed 16-byte tag.
    let mut diff = 0u8;
    for (a, b) in got.iter().zip(mac.iter()) {
        diff |= a ^ b;
    }
    diff == 0
}

#[cfg(test)]
mod tests {
    use super::*;
    use hmac::{Hmac, Mac};
    use sha2::Sha256;

    #[test]
    fn truncated_hmac_vector() {
        let key = b"0123456789abcdef";
        let msg = b"header-and-payload";
        let got = sum(key, msg);
        let want = [
            0x47, 0xa1, 0x79, 0x98, 0x56, 0x0d, 0xb6, 0x4a, 0xfd, 0xd7, 0xbb, 0xe3, 0x08, 0x8d,
            0xfd, 0x97,
        ];
        assert_eq!(got, want);

        let mut mac = Hmac::<Sha256>::new_from_slice(key).unwrap();
        mac.update(msg);
        let full = mac.finalize().into_bytes();
        assert_eq!(full.len(), 32);
        assert_eq!(&full[..SIZE], &got);
        assert!(verify(key, msg, &got));
        let mut bad = got;
        bad[0] ^= 0xff;
        assert!(!verify(key, msg, &bad));
        assert!(!verify(key, msg, &got[..SIZE - 1]));
        let other = sum(key, b"other");
        assert_ne!(other, got);
    }
}

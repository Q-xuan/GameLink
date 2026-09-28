//! GameLink Windows client library. The UDP header matches the Go server.

pub mod control;
pub mod device;
pub mod frame;
pub mod invite;
pub mod ipv4;
pub mod link;
pub mod mac;
pub mod plan;
pub mod platform;
pub mod protocol;
pub mod proxy;
pub mod session;
pub mod settings;
pub mod wintun_pin;

pub use protocol::{Header, FLAG_MAC, HEADER_SIZE, MAC_SIZE, TOKEN_SIZE, VERSION};

#[cfg(test)]
mod manifest_test {
    #[test]
    fn embedded_manifest_requires_administrator() {
        let text = include_str!("../app.manifest");
        assert!(text.contains("level=\"requireAdministrator\""));
        assert!(!text.contains("asInvoker"));
    }
}

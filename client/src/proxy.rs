//! Optional DIRECT hint. This module never edits a proxy file.

use std::net::Ipv4Addr;

pub const RULE_PROCESS: &str = "PROCESS-NAME,gamelink.exe,DIRECT";

pub const ADVICE: &str = "建议直连，也可以用能转发 UDP 的节点（例如 Hysteria2、TUIC）。";

pub const DETECTED: &str =
    "检测到代理 TUN。可以把下面的规则复制到代理规则列表最前面。程序不会修改代理配置。";

/// The single sentence shown when the handshake does not finish in time.
pub const HANDSHAKE_DROPPED: &str = "握手没有完成，若正在使用代理，节点可能丢弃了 UDP。";

const PROXY_PROCESSES: &[&str] = &[
    "FlClash.exe",
    "clash-verge.exe",
    "verge-mihomo.exe",
    "mihomo.exe",
];

pub fn is_proxy_process(name: &str) -> bool {
    PROXY_PROCESSES
        .iter()
        .any(|want| want.eq_ignore_ascii_case(name))
}

/// Build copy-only rules from the relay the user saved locally.
/// A hostname, or no relay, does not produce an IP rule.
pub fn direct_rules(relay: Option<&str>) -> String {
    match relay_ipv4(relay) {
        Some(ip) => format!("{RULE_PROCESS}\nIP-CIDR,{ip}/32,DIRECT,no-resolve"),
        None => RULE_PROCESS.to_string(),
    }
}

pub fn relay_ipv4(relay: Option<&str>) -> Option<Ipv4Addr> {
    let relay = relay?.trim();
    if relay.is_empty() {
        return None;
    }
    let host = if let Some(rest) = relay.strip_prefix('[') {
        rest.split(']').next()?
    } else {
        relay.rsplit_once(':')?.0
    };
    host.parse().ok()
}

pub fn contains_fake_ip(addrs: &[Ipv4Addr]) -> bool {
    addrs.iter().any(|ip| {
        let o = ip.octets();
        o[0] == 198 && (o[1] == 18 || o[1] == 19)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rules_follow_the_saved_relay() {
        assert_eq!(direct_rules(None), RULE_PROCESS);
        assert_eq!(direct_rules(Some("")), RULE_PROCESS);
        assert_eq!(direct_rules(Some("relay.example:41000")), RULE_PROCESS);
        let lines = direct_rules(Some("203.0.113.10:41000"));
        assert_eq!(
            lines,
            "PROCESS-NAME,gamelink.exe,DIRECT\nIP-CIDR,203.0.113.10/32,DIRECT,no-resolve"
        );
        assert!(!direct_rules(None).contains("IP-CIDR"));
    }

    #[test]
    fn copy_is_a_recommendation() {
        for s in [ADVICE, DETECTED, HANDSHAKE_DROPPED] {
            for bad in [
                "必须直连",
                "必须走直连",
                "请改为直连",
                "需要直连",
                "只能直连",
            ] {
                assert!(!s.contains(bad), "{s} contains {bad}");
            }
        }
        assert!(ADVICE.contains("建议直连"));
        assert!(ADVICE.contains("Hysteria2"));
        assert!(ADVICE.contains("TUIC"));
        assert_eq!(HANDSHAKE_DROPPED.chars().filter(|c| *c == '。').count(), 1);
        assert!(HANDSHAKE_DROPPED.contains("UDP"));
        assert!(DETECTED.contains("最前面"));
        assert!(DETECTED.contains("不会修改代理配置"));
    }

    #[test]
    fn fake_ip_and_process_names() {
        assert!(contains_fake_ip(&[Ipv4Addr::new(198, 18, 0, 1)]));
        assert!(contains_fake_ip(&[Ipv4Addr::new(198, 19, 255, 255)]));
        assert!(!contains_fake_ip(&[
            Ipv4Addr::new(198, 20, 0, 1),
            Ipv4Addr::new(10, 66, 0, 1)
        ]));
        for name in [
            "FlClash.exe",
            "clash-verge.exe",
            "verge-mihomo.exe",
            "mihomo.exe",
            "MIHOMO.EXE",
        ] {
            assert!(is_proxy_process(name), "{name}");
        }
        assert!(!is_proxy_process("gamelink.exe"));
        assert!(!is_proxy_process("chrome.exe"));
    }
}

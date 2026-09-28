//! The only route the client may install: 10.66.0.0/24 on-link.
//! Nothing here touches DNS or any other adapter.

use std::net::Ipv4Addr;

use crate::ipv4;

pub const ADAPTER_NAME: &str = "GameLink";
pub const MTU: u32 = 1280;
pub const PREFIX: Ipv4Addr = Ipv4Addr::new(10, 66, 0, 0);
pub const PREFIX_LEN: u8 = 24;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Plan {
    pub adapter: &'static str,
    pub address: Ipv4Addr,
    pub address_len: u8,
    pub mtu: u32,
    pub route: Ipv4Addr,
    pub route_len: u8,
    pub on_link: bool,
}

impl Plan {
    pub fn has_default_route(&self) -> bool {
        self.route_len == 0
    }

    pub fn is_safe(&self) -> bool {
        self.adapter == ADAPTER_NAME
            && self.on_link
            && self.route_len == PREFIX_LEN
            && self.route == PREFIX
            && self.address_len == PREFIX_LEN
            && self.mtu == MTU
            && !self.has_default_route()
    }
}

pub fn for_vip(vip: Ipv4Addr) -> Result<Plan, String> {
    let id = u32::from(vip.octets()[3]);
    match ipv4::virtual_ip(id) {
        Some(got) if got == vip => {}
        _ => return Err(format!("虚拟地址 {vip} 不在 10.66.0.1–8")),
    }
    let plan = Plan {
        adapter: ADAPTER_NAME,
        address: vip,
        address_len: PREFIX_LEN,
        mtu: MTU,
        route: PREFIX,
        route_len: PREFIX_LEN,
        on_link: true,
    };
    if !plan.is_safe() {
        return Err("拒绝安装默认路由".into());
    }
    Ok(plan)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn plan_only_on_link_subnet() {
        for vip in ["10.66.0.1", "10.66.0.8"] {
            let plan = for_vip(vip.parse().unwrap()).unwrap();
            assert_eq!(plan.adapter, "GameLink");
            assert_eq!(plan.mtu, 1280);
            assert_eq!(plan.address.to_string() + "/24", format!("{vip}/24"));
            assert_eq!(plan.route, Ipv4Addr::new(10, 66, 0, 0));
            assert_eq!(plan.route_len, 24);
            assert!(plan.on_link);
            assert!(!plan.has_default_route());
            assert!(plan.is_safe());
            assert_ne!(plan.route_len, 0);
        }
    }

    #[test]
    fn plan_rejects_outside_subnet() {
        for vip in [
            "10.66.0.0",
            "10.66.0.9",
            "10.66.1.1",
            "0.0.0.0",
            "192.168.1.1",
        ] {
            assert!(for_vip(vip.parse().unwrap()).is_err(), "{vip}");
        }
    }
}

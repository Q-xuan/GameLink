//! First-launch server addresses, stored under the current user's AppData.

use std::fs;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::control;

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Settings {
    pub control_url: String,
    pub relay: String,
}

pub fn user_path() -> PathBuf {
    if let Ok(appdata) = std::env::var("APPDATA") {
        if !appdata.is_empty() {
            return PathBuf::from(appdata)
                .join("GameLink")
                .join("settings.json");
        }
    }
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".into());
    PathBuf::from(home)
        .join(".config")
        .join("GameLink")
        .join("settings.json")
}

pub fn load(path: &Path) -> Option<Settings> {
    let text = fs::read_to_string(path).ok()?;
    let settings: Settings = serde_json::from_str(&text).ok()?;
    validate(&settings.control_url, &settings.relay).ok()
}

pub fn save(path: &Path, settings: &Settings) -> Result<(), String> {
    let checked = validate(&settings.control_url, &settings.relay)?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|_| "无法创建 AppData 目录".to_string())?;
    }
    let body = serde_json::to_vec_pretty(&checked).map_err(|_| "无法保存设置".to_string())?;
    let mut tmp = path.to_path_buf();
    tmp.set_extension("json.tmp");
    fs::write(&tmp, &body).map_err(|_| "无法保存设置".to_string())?;
    fs::rename(&tmp, path).map_err(|_| "无法保存设置".to_string())?;
    Ok(())
}

pub fn validate(control_url: &str, relay: &str) -> Result<Settings, String> {
    let control_url = control::http_base(control_url.trim())?;
    let relay = normalize_relay(relay.trim())?;
    Ok(Settings { control_url, relay })
}

pub fn normalize_relay(relay: &str) -> Result<String, String> {
    let relay = relay.trim();
    let (host, port, v6) = split_host_port(relay).ok_or("中继地址需要写成主机:端口")?;
    if host.is_empty() {
        return Err("中继地址需要写成主机:端口".into());
    }
    let port_n: u16 = port
        .parse()
        .map_err(|_| "中继地址需要写成主机:端口".to_string())?;
    if port_n == 0 {
        return Err("中继地址需要写成主机:端口".into());
    }
    if v6 {
        Ok(format!("[{host}]:{port}"))
    } else {
        Ok(format!("{host}:{port}"))
    }
}

fn split_host_port(relay: &str) -> Option<(&str, &str, bool)> {
    if let Some(rest) = relay.strip_prefix('[') {
        let (host, port) = rest.split_once("]:")?;
        Some((host, port, true))
    } else {
        let (host, port) = relay.rsplit_once(':')?;
        Some((host, port, false))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roundtrip_has_no_token_field() {
        let dir = std::env::temp_dir().join(format!("gamelink-settings-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        let path = dir.join("settings.json");
        let settings = validate("http://127.0.0.1:41080", "127.0.0.1:41000").unwrap();
        save(&path, &settings).unwrap();
        let text = fs::read_to_string(&path).unwrap();
        assert!(text.contains("127.0.0.1:41000"));
        assert!(!text.to_ascii_lowercase().contains("token"));
        assert_eq!(load(&path).unwrap(), settings);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn reject_incomplete_addresses() {
        assert!(validate("ftp://127.0.0.1", "127.0.0.1:1").is_err());
        assert!(validate("http://127.0.0.1:41080", "127.0.0.1").is_err());
        assert!(validate("", "").is_err());
        assert_eq!(
            normalize_relay("[2001:db8::1]:41000").unwrap(),
            "[2001:db8::1]:41000"
        );
    }
}

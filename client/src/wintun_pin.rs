//! Official Wintun 0.14.1 amd64 DLL, checked before it is written or loaded.

use std::fs;
use std::path::{Path, PathBuf};

use sha2::{Digest, Sha256};

pub const VERSION: &str = "0.14.1";
pub const ARCH: &str = "amd64";
pub const ZIP_SHA256: &str = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51";
pub const DLL_SHA256: &str = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce";
pub const FILE_NAME: &str = "wintun.dll";

const EMBEDDED: &[u8] = include_bytes!("../assets/wintun.dll");

pub fn embedded_dll() -> &'static [u8] {
    EMBEDDED
}

pub fn sha256_hex(bytes: &[u8]) -> String {
    let sum = Sha256::digest(bytes);
    hex::encode(sum)
}

pub fn verify_bytes(bytes: &[u8]) -> Result<(), String> {
    let got = sha256_hex(bytes);
    if got != DLL_SHA256 {
        return Err("内置 wintun.dll 与官方 Wintun 0.14.1 amd64 校验值不一致，拒绝启动".into());
    }
    Ok(())
}

/// Write the embedded DLL into `dir` after checking both the embedded bytes
/// and the file that will be loaded. The Wintun loader needs that file beside
/// the executable. The release asset is still only the exe.
pub fn stage_verified(dir: &Path) -> Result<PathBuf, String> {
    verify_bytes(EMBEDDED)?;
    fs::create_dir_all(dir).map_err(|_| "无法写出 wintun.dll".to_string())?;
    let dest = dir.join(FILE_NAME);
    if let Ok(existing) = fs::read(&dest) {
        if sha256_hex(&existing) == DLL_SHA256 {
            return Ok(dest);
        }
    }
    let tmp = dir.join("wintun.dll.tmp");
    fs::write(&tmp, EMBEDDED).map_err(|_| "无法写出 wintun.dll".to_string())?;
    if fs::rename(&tmp, &dest).is_err() {
        let _ = fs::remove_file(&dest);
        fs::rename(&tmp, &dest).map_err(|_| "无法替换 wintun.dll".to_string())?;
    }
    let written = fs::read(&dest).map_err(|_| "写出的 wintun.dll 无法读取".to_string())?;
    if sha256_hex(&written) != DLL_SHA256 {
        let _ = fs::remove_file(&dest);
        return Err("写出的 wintun.dll 校验失败，拒绝启动".into());
    }
    Ok(dest)
}

pub fn stage_beside_exe() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|_| "无法定位程序自身".to_string())?;
    let dir = exe.parent().ok_or_else(|| "无法定位程序自身".to_string())?;
    stage_verified(dir)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn embedded_dll_matches_pin() {
        assert_eq!(sha256_hex(embedded_dll()), DLL_SHA256);
        assert_eq!(
            DLL_SHA256,
            "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
        );
        assert_eq!(
            ZIP_SHA256,
            "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
        );
        assert_eq!(VERSION, "0.14.1");
        assert_eq!(ARCH, "amd64");
    }

    #[test]
    fn stage_writes_a_matching_file() {
        let dir = std::env::temp_dir().join(format!("gamelink-wintun-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        let path = stage_verified(&dir).unwrap();
        assert_eq!(sha256_hex(&fs::read(&path).unwrap()), DLL_SHA256);
        let again = stage_verified(&dir).unwrap();
        assert_eq!(again, path);
        let _ = fs::remove_dir_all(&dir);
    }
}

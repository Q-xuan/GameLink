//! Invite string. The canonical form is `gamelink://join/<code>/<token>`.

use crate::ipv4;
use crate::protocol::TOKEN_SIZE;

const ALPHABET: &str = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const CODE_LEN: usize = 6;

pub fn format_invite(code: &str, token: &str) -> String {
    format!("gamelink://join/{code}/{token}")
}

pub fn normalize_code(code: &str) -> Result<String, String> {
    let code = code.trim().to_ascii_uppercase();
    if code.len() != CODE_LEN || !code.chars().all(|c| ALPHABET.contains(c)) {
        return Err("房间码无效".into());
    }
    Ok(code)
}

pub fn parse_token(s: &str) -> Result<[u8; TOKEN_SIZE], String> {
    let s = s.trim();
    let raw = hex::decode(s).map_err(|_| "令牌无效".to_string())?;
    if raw.len() != TOKEN_SIZE {
        return Err("令牌无效".into());
    }
    let mut out = [0u8; TOKEN_SIZE];
    out.copy_from_slice(&raw);
    Ok(out)
}

pub fn parse(raw: &str) -> Result<(String, String), String> {
    let s = trim_invite(raw);
    if s.is_empty() {
        return Err("邀请串是空的".into());
    }
    if s.contains("://") {
        return parse_url(&s);
    }
    let fields: Vec<&str> = s
        .split(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '/' | ',' | '|'))
        .filter(|p| !p.is_empty())
        .collect();
    if fields.len() < 2 {
        return Err("邀请串里需要房间码和令牌".into());
    }
    normalize(fields[0], fields[1])
}

fn parse_url(s: &str) -> Result<(String, String), String> {
    let u = url::Url::parse(s).map_err(|_| "无法识别邀请链接".to_string())?;
    if !u.scheme().eq_ignore_ascii_case("gamelink") {
        return Err("无法识别邀请链接".into());
    }
    let mut parts: Vec<String> = Vec::new();
    if let Some(host) = u.host_str() {
        parts.push(host.to_string());
    }
    if !u.path().is_empty() {
        parts.extend(
            u.path()
                .trim_matches('/')
                .split('/')
                .filter(|p| !p.is_empty())
                .map(|p| p.to_string()),
        );
    }
    if parts.len() >= 3 && parts[0].eq_ignore_ascii_case("join") {
        return normalize(&parts[1], &parts[2]);
    }
    if parts.len() >= 2 {
        let n = parts.len();
        return normalize(&parts[n - 2], &parts[n - 1]);
    }
    Err("邀请链接里需要房间码和令牌".into())
}

fn normalize(code: &str, token: &str) -> Result<(String, String), String> {
    let code = normalize_code(code)?;
    let raw = parse_token(token)?;
    let _ = ipv4::MAX_PEERS;
    Ok((code, hex::encode(raw)))
}

fn trim_invite(s: &str) -> String {
    s.trim().trim_matches(|c| c == '"' || c == '\'').to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    const TOKEN: &str = "0123456789abcdef0123456789abcdef";

    #[test]
    fn format_and_parse_url() {
        let s = format_invite("AB2345", TOKEN);
        assert_eq!(s, format!("gamelink://join/AB2345/{TOKEN}"));
        let wrapped = format!("  \"{s}\"  ");
        let (code, token) = parse(&wrapped).unwrap();
        assert_eq!(code, "AB2345");
        assert_eq!(token, TOKEN);
    }

    #[test]
    fn parse_separated_fields() {
        let (code, token) = parse("ab2345 0123456789ABCDEF0123456789ABCDEF").unwrap();
        assert_eq!(code, "AB2345");
        assert_eq!(token, TOKEN);
    }

    #[test]
    fn reject_bad_code_and_token() {
        assert!(parse("gamelink://join/IIIIII/0123456789abcdef0123456789abcdef").is_err());
        assert!(parse("gamelink://join/AB2345/abcd").is_err());
        assert!(parse("").is_err());
        assert!(normalize_code("10.66.0.1").is_err());
    }
}

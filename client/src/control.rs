//! Control-plane HTTP. The timeout is at least 10 seconds.
//! UDP is dialed separately, so the two source addresses may differ.

use std::time::Duration;

use serde::Deserialize;

pub const HTTP_TIMEOUT: Duration = Duration::from_secs(15);

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Membership {
    pub code: String,
    pub token: String,
    pub room_id: u64,
    pub peer_id: u32,
    pub vip: String,
    /// Relay address returned by the server. The GUI dials the saved relay instead.
    pub server_relay: String,
}

#[derive(Deserialize)]
struct RoomBody {
    code: String,
    #[serde(default)]
    token: String,
    room_id: String,
    peer_id: u32,
    vip: String,
    relay: String,
}

pub fn http_base(raw: &str) -> Result<String, String> {
    rewrite_scheme(raw, true)
}

pub fn ws_base(raw: &str) -> Result<String, String> {
    rewrite_scheme(raw, false)
}

fn rewrite_scheme(raw: &str, http: bool) -> Result<String, String> {
    let mut u = url::Url::parse(raw.trim()).map_err(|_| "控制面地址无效".to_string())?;
    if u.host_str().is_none() {
        return Err("控制面地址无效".into());
    }
    let next = match (u.scheme(), http) {
        ("https" | "wss", true) => "https",
        ("http" | "ws", true) => "http",
        ("https" | "wss", false) => "wss",
        ("http" | "ws", false) => "ws",
        _ => return Err("控制面地址无效".into()),
    };
    u.set_scheme(next)
        .map_err(|_| "控制面地址无效".to_string())?;
    u.set_query(None);
    u.set_fragment(None);
    let mut out = u.to_string();
    while out.ends_with('/') {
        out.pop();
    }
    Ok(out)
}

/// The UDP socket uses the address saved on this computer.
pub fn choose_relay<'a>(saved: &'a str, _server_relay: &str) -> &'a str {
    saved
}

pub fn create_room(control_url: &str) -> Result<Membership, String> {
    let base = http_base(control_url)?;
    let url = format!("{base}/v1/rooms");
    let resp = call(agent()?.post(&url).call())?;
    expect_status(&resp, 201)?;
    parse_body(resp, true)
}

pub fn join_room(control_url: &str, code: &str, token: &str) -> Result<Membership, String> {
    let base = http_base(control_url)?;
    let url = format!("{base}/v1/rooms/{code}/join");
    let resp = call(
        agent()?
            .post(&url)
            .send_json(serde_json::json!({ "token": token })),
    )?;
    expect_status(&resp, 200)?;
    let mut membership = parse_body(resp, false)?;
    if membership.token.is_empty() {
        membership.token = token.to_string();
    }
    Ok(membership)
}

fn agent() -> Result<ureq::Agent, String> {
    let mut builder = ureq::AgentBuilder::new().timeout(HTTP_TIMEOUT);
    if let Some(proxy) = http_proxy_from_env() {
        let proxy = ureq::Proxy::new(proxy).map_err(|_| "HTTP 代理地址无效".to_string())?;
        builder = builder.proxy(proxy);
    }
    Ok(builder.build())
}

fn call(result: Result<ureq::Response, ureq::Error>) -> Result<ureq::Response, String> {
    match result {
        Ok(resp) => Ok(resp),
        Err(ureq::Error::Status(code, _)) => Err(status_message(code)),
        Err(_) => Err("控制面连接失败".into()),
    }
}

fn expect_status(resp: &ureq::Response, want: u16) -> Result<(), String> {
    if resp.status() == want {
        Ok(())
    } else {
        Err(status_message(resp.status()))
    }
}

fn status_message(code: u16) -> String {
    match code {
        400 => "请求无效".into(),
        401 | 403 => "令牌无效".into(),
        404 => "房间不存在".into(),
        409 => "房间已满".into(),
        _ => format!("控制面返回 {code}"),
    }
}

fn parse_body(resp: ureq::Response, require_token: bool) -> Result<Membership, String> {
    let body: RoomBody = resp.into_json().map_err(|_| "控制面响应无效".to_string())?;
    if require_token && body.token.is_empty() {
        return Err("控制面响应无效".into());
    }
    let room_id = body
        .room_id
        .parse()
        .map_err(|_| "控制面响应无效".to_string())?;
    Ok(Membership {
        code: body.code,
        token: body.token,
        room_id,
        peer_id: body.peer_id,
        vip: body.vip,
        server_relay: body.relay,
    })
}

fn http_proxy_from_env() -> Option<String> {
    std::env::var("HTTPS_PROXY")
        .ok()
        .filter(|s| !s.is_empty())
        .or_else(|| std::env::var("https_proxy").ok())
        .or_else(|| std::env::var("HTTP_PROXY").ok())
        .or_else(|| std::env::var("http_proxy").ok())
        .or_else(|| std::env::var("ALL_PROXY").ok())
        .or_else(|| std::env::var("all_proxy").ok())
        .filter(|s| !s.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;

    #[test]
    fn http_timeout_covers_ten_seconds() {
        assert!(HTTP_TIMEOUT >= Duration::from_secs(10));
        assert!(HTTP_TIMEOUT >= Duration::from_secs(15));
    }

    #[test]
    fn scheme_rewrite_and_saved_relay() {
        assert_eq!(
            http_base("wss://127.0.0.1:41080").unwrap(),
            "https://127.0.0.1:41080"
        );
        assert_eq!(
            ws_base("http://127.0.0.1:41080/").unwrap(),
            "ws://127.0.0.1:41080"
        );
        assert_eq!(choose_relay("127.0.0.1:9", "10.1.2.3:41000"), "127.0.0.1:9");
        assert!(http_base("ftp://127.0.0.1").is_err());
    }

    #[test]
    fn create_parses_local_response() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        thread::spawn(move || {
            let (mut sock, _) = listener.accept().unwrap();
            let mut buf = [0u8; 2048];
            let _ = sock.read(&mut buf);
            let body = r#"{"code":"AB2345","token":"0123456789abcdef0123456789abcdef","room_id":"42","peer_id":1,"vip":"10.66.0.1","relay":"127.0.0.1:41000","control_url":"http://127.0.0.1:41080"}"#;
            let resp = format!(
                "HTTP/1.1 201 Created\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            );
            let _ = sock.write_all(resp.as_bytes());
        });
        let got = create_room(&format!("http://{addr}")).unwrap();
        assert_eq!(got.code, "AB2345");
        assert_eq!(got.room_id, 42);
        assert_eq!(got.peer_id, 1);
        assert_eq!(got.vip, "10.66.0.1");
        assert_eq!(
            choose_relay("203.0.113.10:41000", &got.server_relay),
            "203.0.113.10:41000"
        );
    }
}

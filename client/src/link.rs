//! Room session: control call, handshake, ping, and unicast IPv4 forward.

use std::net::Ipv4Addr;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use crate::control::{self, Membership};
use crate::device::PacketIo;
use crate::invite::{self, format_invite};
use crate::ipv4::{self, dest_peer};
use crate::plan;
use crate::platform;
use crate::protocol::{TYPE_DATA, TYPE_PING, TYPE_PONG};
use crate::proxy::HANDSHAKE_DROPPED;
use crate::session::{SessionError, UdpSession, HANDSHAKE_TIMEOUT, PING_EVERY};
use crate::settings::Settings;

#[derive(Clone, Debug)]
pub struct Snapshot {
    pub status: String,
    pub vip: String,
    pub handshake: String,
    pub latency_ms: Option<u32>,
    pub invite: String,
    pub notice: String,
}

impl Default for Snapshot {
    fn default() -> Self {
        Self {
            status: "未连接".into(),
            vip: "—".into(),
            handshake: "—".into(),
            latency_ms: None,
            invite: String::new(),
            notice: String::new(),
        }
    }
}

#[derive(Clone, Debug)]
pub enum Job {
    Create,
    Join { code: String, token: String },
}

pub struct Running {
    stop: Arc<AtomicBool>,
    snap: Arc<Mutex<Snapshot>>,
    worker: Option<JoinHandle<()>>,
}

impl Running {
    pub fn start(settings: Settings, job: Job) -> Self {
        let stop = Arc::new(AtomicBool::new(false));
        let snap = Arc::new(Mutex::new(Snapshot::default()));
        let stop_worker = stop.clone();
        let snap_worker = snap.clone();
        let worker = thread::spawn(move || drive(settings, job, stop_worker, snap_worker));
        Self {
            stop,
            snap,
            worker: Some(worker),
        }
    }

    pub fn snapshot(&self) -> Snapshot {
        lock(&self.snap).clone()
    }

    pub fn stop(&mut self) {
        self.stop.store(true, Ordering::Relaxed);
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

impl Drop for Running {
    fn drop(&mut self) {
        self.stop();
    }
}

fn drive(settings: Settings, job: Job, stop: Arc<AtomicBool>, snap: Arc<Mutex<Snapshot>>) {
    if let Err(err) = drive_inner(&settings, job, &stop, &snap) {
        let mut view = lock(&snap);
        if view.handshake != HANDSHAKE_DROPPED {
            view.notice = err;
        }
        if view.status.starts_with("正在") {
            view.status = "未连接".into();
        }
    }
}

fn drive_inner(
    settings: &Settings,
    job: Job,
    stop: &AtomicBool,
    snap: &Arc<Mutex<Snapshot>>,
) -> Result<(), String> {
    {
        let mut view = lock(snap);
        view.status = "正在连接".into();
        view.notice.clear();
        view.handshake = "—".into();
        view.latency_ms = None;
    }
    let created = matches!(job, Job::Create);
    let membership = match job {
        Job::Create => control::create_room(&settings.control_url)?,
        Job::Join { code, token } => {
            let (code, token) = invite::parse(&format!("{code} {token}"))
                .or_else(|_| invite::parse(&format!("gamelink://join/{code}/{token}")))?;
            control::join_room(&settings.control_url, &code, &token)?
        }
    };
    if stop.load(Ordering::Relaxed) {
        return Ok(());
    }
    publish_membership(snap, &membership, created);
    let token = invite::parse_token(&membership.token)?;
    let vip: Ipv4Addr = membership
        .vip
        .parse()
        .map_err(|_| "虚拟地址无效".to_string())?;
    plan::for_vip(vip)?;
    let relay = control::choose_relay(&settings.relay, &membership.server_relay);
    {
        let mut view = lock(snap);
        view.handshake = "正在握手".into();
    }
    let session = UdpSession::dial(relay, None, membership.room_id, membership.peer_id, token)
        .map_err(|err| err.to_string())?;
    match session.handshake(HANDSHAKE_TIMEOUT, stop) {
        Ok(()) => {}
        Err(SessionError::HandshakeTimeout) => {
            let mut view = lock(snap);
            view.handshake = HANDSHAKE_DROPPED.into();
            view.status = "未连接".into();
            return Ok(());
        }
        Err(SessionError::Timeout) => return Ok(()),
        Err(err) => return Err(err.to_string()),
    }
    if stop.load(Ordering::Relaxed) {
        return Ok(());
    }
    {
        let mut view = lock(snap);
        view.handshake = "握手完成".into();
        view.status = "已连接".into();
    }
    let device = platform::open_tunnel(vip)?;
    {
        let mut view = lock(snap);
        view.handshake = "正在 Ping".into();
    }
    run_dataplane(session, device, vip, stop, snap);
    if !stop.load(Ordering::Relaxed) {
        let mut view = lock(snap);
        view.status = "未连接".into();
    }
    Ok(())
}

fn publish_membership(snap: &Arc<Mutex<Snapshot>>, membership: &Membership, created: bool) {
    let mut view = lock(snap);
    view.vip = format!("{}/24", membership.vip);
    view.status = format!("房间 {}", membership.code);
    if created {
        view.invite = format_invite(&membership.code, &membership.token);
    }
}

pub fn run_dataplane(
    session: UdpSession,
    device: Arc<dyn PacketIo>,
    local: Ipv4Addr,
    stop: &AtomicBool,
    snap: &Arc<Mutex<Snapshot>>,
) {
    let session = Arc::new(session);
    let pending = Arc::new(Mutex::new(None::<(u32, Instant)>));
    let failed = Arc::new(AtomicBool::new(false));
    let session_ping = session.clone();
    let pending_ping = pending.clone();
    let stop_ping = Arc::new(AtomicBool::new(false));
    let stop_ping_flag = stop_ping.clone();
    let ping = thread::spawn(move || ping_loop(session_ping, pending_ping, &stop_ping_flag));

    let session_out = session.clone();
    let device_out = device.clone();
    let stop_out = stop_ping.clone();
    let failed_out = failed.clone();
    let outbound = thread::spawn(move || {
        if pump_out(session_out, device_out, local, &stop_out).is_err() {
            failed_out.store(true, Ordering::Relaxed);
        }
    });

    let session_in = session.clone();
    let device_in = device;
    let pending_in = pending;
    let snap_in = snap.clone();
    let stop_in = stop_ping.clone();
    let failed_in = failed.clone();
    let inbound = thread::spawn(move || {
        if pump_in(session_in, device_in, pending_in, &snap_in, &stop_in).is_err() {
            failed_in.store(true, Ordering::Relaxed);
            lock(&snap_in).notice = "转发失败".into();
        }
    });

    while !stop.load(Ordering::Relaxed) && !failed.load(Ordering::Relaxed) {
        thread::sleep(Duration::from_millis(50));
    }
    stop_ping.store(true, Ordering::Relaxed);
    let _ = outbound.join();
    let _ = inbound.join();
    let _ = ping.join();
    if failed.load(Ordering::Relaxed) {
        let mut view = lock(snap);
        if view.notice.is_empty() {
            view.notice = "转发失败".into();
        }
        view.status = "未连接".into();
    }
}

fn ping_loop(
    session: Arc<UdpSession>,
    pending: Arc<Mutex<Option<(u32, Instant)>>>,
    stop: &AtomicBool,
) {
    while !stop.load(Ordering::Relaxed) {
        if let Ok(seq) = session.send(TYPE_PING, 0, b"") {
            *lock(&pending) = Some((seq, Instant::now()));
        }
        if sleep_stop(stop, PING_EVERY) {
            break;
        }
    }
}

fn pump_out(
    session: Arc<UdpSession>,
    device: Arc<dyn PacketIo>,
    local: Ipv4Addr,
    stop: &AtomicBool,
) -> Result<(), ()> {
    while !stop.load(Ordering::Relaxed) {
        match device.read_packet(Duration::from_millis(200)) {
            Ok(Some(pkt)) => {
                if let Some(dst) = dest_peer(&pkt, local) {
                    if session.send(TYPE_DATA, dst, &pkt).is_err() {
                        return Err(());
                    }
                }
            }
            Ok(None) => {}
            Err(_) => return Err(()),
        }
    }
    Ok(())
}

fn pump_in(
    session: Arc<UdpSession>,
    device: Arc<dyn PacketIo>,
    pending: Arc<Mutex<Option<(u32, Instant)>>>,
    snap: &Mutex<Snapshot>,
    stop: &AtomicBool,
) -> Result<(), ()> {
    while !stop.load(Ordering::Relaxed) {
        match session.recv_deadline(Duration::from_millis(200)) {
            Ok((header, payload)) => {
                if header.typ == TYPE_PONG {
                    note_pong(&pending, snap, header.sequence);
                } else if header.typ == TYPE_DATA && ipv4::deliverable(&payload) {
                    if device.write_packet(&payload).is_err() {
                        return Err(());
                    }
                }
            }
            Err(SessionError::Timeout) => {}
            Err(_) => return Err(()),
        }
    }
    Ok(())
}

fn note_pong(pending: &Mutex<Option<(u32, Instant)>>, snap: &Mutex<Snapshot>, sequence: u32) {
    let stamped = lock(pending);
    if let Some((seq, sent)) = *stamped {
        if seq == sequence {
            let ms = sent.elapsed().as_millis().min(u128::from(u32::MAX)) as u32;
            lock(snap).latency_ms = Some(ms);
        }
    }
}

fn sleep_stop(stop: &AtomicBool, total: Duration) -> bool {
    let end = Instant::now() + total;
    while Instant::now() < end {
        if stop.load(Ordering::Relaxed) {
            return true;
        }
        thread::sleep(Duration::from_millis(50));
    }
    false
}

fn lock<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|err| err.into_inner())
}

#[cfg(test)]
mod tests {
    use super::*;

    struct Queue {
        inbound: Mutex<Vec<Vec<u8>>>,
    }

    impl PacketIo for Queue {
        fn read_packet(&self, _timeout: Duration) -> std::io::Result<Option<Vec<u8>>> {
            Ok(self.inbound.lock().unwrap().pop())
        }
        fn write_packet(&self, packet: &[u8]) -> std::io::Result<()> {
            self.inbound.lock().unwrap().push(packet.to_vec());
            Ok(())
        }
    }

    #[test]
    fn outbound_filter_matches_go_forwarder() {
        let local = Ipv4Addr::new(10, 66, 0, 1);
        let keep = ipv4::sample_packet(17, local, Ipv4Addr::new(10, 66, 0, 2), b"ok");
        assert_eq!(dest_peer(&keep, local), Some(2));
        let self_pkt = ipv4::sample_packet(1, local, local, &[1]);
        assert_eq!(dest_peer(&self_pkt, local), None);
        let _device = Queue {
            inbound: Mutex::new(vec![keep]),
        };
    }
}

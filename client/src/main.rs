//! GameLink window. iced draws the Chinese product UI.

use iced::widget::{button, column, container, row, scrollable, text, text_input};
use iced::{Element, Font, Length, Subscription, Task, Theme};

use gamelink::invite;
use gamelink::link::{Job, Running, Snapshot};
use gamelink::platform;
use gamelink::proxy::{self, ADVICE, DETECTED};
use gamelink::settings;

const UI_FONT: &[u8] = include_bytes!("../assets/ui.otf");

fn main() -> iced::Result {
    let pending = std::env::args()
        .skip(1)
        .find(|arg| arg.to_ascii_lowercase().contains("gamelink://"));
    iced::application(App::title, App::update, App::view)
        .subscription(App::subscription)
        .font(UI_FONT)
        .default_font(Font::with_name("Noto Sans CJK SC"))
        .window_size((440.0, 640.0))
        .centered()
        .theme(|_app: &App| Theme::Light)
        .run_with(move || App::new(pending))
}

struct App {
    control: String,
    relay: String,
    join_text: String,
    show_join: bool,
    saved: bool,
    form_error: String,
    notice: String,
    banner: String,
    elevated: bool,
    proxy: bool,
    running: Option<Running>,
    snap: Snapshot,
}

#[derive(Clone, Debug)]
enum Message {
    ControlChanged(String),
    RelayChanged(String),
    JoinChanged(String),
    Save,
    EditServer,
    Create,
    ToggleJoin,
    ConfirmJoin,
    Leave,
    CopyInvite,
    CopyRules,
    Tick,
}

impl App {
    fn new(pending: Option<String>) -> (Self, Task<Message>) {
        let mut banner = String::new();
        if let Err(err) = platform::register_protocol() {
            banner = err;
        }
        let elevated = platform::is_elevated();
        if !elevated {
            banner = "需要管理员权限才能创建 GameLink 网卡。请在 UAC 提示中允许。".into();
        }
        let stored = settings::load(&settings::user_path());
        let saved = stored.is_some();
        let (control, relay) = match stored {
            Some(settings) => (settings.control_url, settings.relay),
            None => (String::new(), String::new()),
        };
        let mut app = Self {
            control,
            relay,
            join_text: pending.unwrap_or_default(),
            show_join: false,
            saved,
            form_error: String::new(),
            notice: String::new(),
            banner,
            elevated,
            proxy: platform::proxy_tun_present(),
            running: None,
            snap: Snapshot::default(),
        };
        if app.saved && app.elevated && invite::parse(&app.join_text).is_ok() {
            app.start(Job::Join {
                code: app.join_text.clone(),
                token: String::new(),
            });
        } else if !app.join_text.is_empty() {
            app.show_join = true;
        }
        (app, Task::none())
    }

    fn title(&self) -> String {
        "GameLink".into()
    }

    fn subscription(&self) -> Subscription<Message> {
        iced::time::every(std::time::Duration::from_millis(200)).map(|_| Message::Tick)
    }

    fn update(&mut self, message: Message) -> Task<Message> {
        match message {
            Message::ControlChanged(value) => {
                self.control = value;
                self.form_error.clear();
                Task::none()
            }
            Message::RelayChanged(value) => {
                self.relay = value;
                self.form_error.clear();
                Task::none()
            }
            Message::JoinChanged(value) => {
                self.join_text = value;
                self.form_error.clear();
                Task::none()
            }
            Message::Save => {
                match settings::validate(&self.control, &self.relay) {
                    Ok(settings) => {
                        if let Err(err) = settings::save(&settings::user_path(), &settings) {
                            self.form_error = err;
                            return Task::none();
                        }
                        self.control = settings.control_url;
                        self.relay = settings.relay;
                        self.saved = true;
                        self.form_error.clear();
                        self.notice = "已保存".into();
                        if invite::parse(&self.join_text).is_ok() {
                            let invite = self.join_text.clone();
                            self.start(Job::Join {
                                code: invite,
                                token: String::new(),
                            });
                        }
                    }
                    Err(err) => self.form_error = err,
                }
                Task::none()
            }
            Message::EditServer => {
                self.stop_session();
                self.saved = false;
                self.notice.clear();
                Task::none()
            }
            Message::Create => {
                self.start(Job::Create);
                Task::none()
            }
            Message::ToggleJoin => {
                self.show_join = !self.show_join;
                Task::none()
            }
            Message::ConfirmJoin => {
                if invite::parse(&self.join_text).is_err() {
                    self.form_error = "邀请串里需要房间码和令牌".into();
                    return Task::none();
                }
                let invite = self.join_text.clone();
                self.start(Job::Join {
                    code: invite,
                    token: String::new(),
                });
                Task::none()
            }
            Message::Leave => {
                self.stop_session();
                self.snap = Snapshot::default();
                self.notice.clear();
                Task::none()
            }
            Message::CopyInvite => {
                let invite = self.snap.invite.clone();
                self.notice = "已复制".into();
                iced::clipboard::write(invite)
            }
            Message::CopyRules => {
                let rules = self.rules();
                self.notice = "已复制".into();
                iced::clipboard::write(rules)
            }
            Message::Tick => {
                if let Some(running) = &self.running {
                    self.snap = running.snapshot();
                    if self.snap.handshake == proxy::HANDSHAKE_DROPPED {
                        self.notice.clear();
                    }
                }
                Task::none()
            }
        }
    }

    fn start(&mut self, mut job: Job) {
        if !self.saved || !self.elevated || self.running.is_some() {
            return;
        }
        if let Job::Join { code, token } = &job {
            if token.is_empty() {
                match invite::parse(code) {
                    Ok((parsed_code, parsed_token)) => {
                        job = Job::Join {
                            code: parsed_code,
                            token: parsed_token,
                        };
                    }
                    Err(err) => {
                        self.form_error = err;
                        return;
                    }
                }
            }
        }
        let Ok(settings) = settings::validate(&self.control, &self.relay) else {
            self.saved = false;
            return;
        };
        self.form_error.clear();
        self.notice.clear();
        self.snap = Snapshot {
            status: "正在连接".into(),
            ..Snapshot::default()
        };
        self.running = Some(Running::start(settings, job));
    }

    fn stop_session(&mut self) {
        if let Some(mut running) = self.running.take() {
            running.stop();
        }
    }

    fn rules(&self) -> String {
        let relay = if self.saved {
            Some(self.relay.as_str())
        } else {
            None
        };
        proxy::direct_rules(relay)
    }

    fn view(&self) -> Element<'_, Message> {
        let body = if self.saved {
            self.home()
        } else {
            self.setup()
        };
        container(scrollable(body).height(Length::Fill))
            .padding(22)
            .width(Length::Fill)
            .height(Length::Fill)
            .into()
    }

    fn setup(&self) -> Element<'_, Message> {
        let mut items: Vec<Element<'_, Message>> = vec![
            text("GameLink").size(28).into(),
            text("请填写控制面地址和中继地址。程序里没有内置服务器，地址只保存在当前用户的 AppData。")
                .width(Length::Fill)
                .into(),
            text("控制面").size(16).into(),
            text_input("http://主机:端口", &self.control)
                .on_input(Message::ControlChanged)
                .padding(10)
                .width(Length::Fill)
                .into(),
            text("中继（主机:端口）").size(16).into(),
            text_input("主机:端口", &self.relay)
                .on_input(Message::RelayChanged)
                .padding(10)
                .width(Length::Fill)
                .into(),
            button(text("保存")).on_press(Message::Save).padding([8, 18]).into(),
        ];
        push_notes(&mut items, self);
        column(items).spacing(12).into()
    }

    fn home(&self) -> Element<'_, Message> {
        let busy = self.running.is_some();
        let mut create = button(text("创建房间")).padding([8, 16]);
        let mut join = button(text("加入房间")).padding([8, 16]);
        if !busy && self.elevated {
            create = create.on_press(Message::Create);
            join = join.on_press(Message::ToggleJoin);
        }
        let mut items: Vec<Element<'_, Message>> = vec![
            text("GameLink").size(28).into(),
            info("状态", self.snap.status.clone()),
            info("虚拟地址", self.snap.vip.clone()),
            info("握手", self.snap.handshake.clone()),
            info("延迟", latency_text(self.snap.latency_ms)),
            row![create, join].spacing(12).into(),
        ];
        if self.show_join && !busy {
            items.push(
                text_input("gamelink://join/房间码/令牌", &self.join_text)
                    .on_input(Message::JoinChanged)
                    .padding(10)
                    .width(Length::Fill)
                    .into(),
            );
            items.push(
                button(text("确认加入"))
                    .on_press(Message::ConfirmJoin)
                    .padding([8, 16])
                    .into(),
            );
        }
        if !self.snap.invite.is_empty() {
            items.push(text("邀请").size(16).into());
            items.push(text(self.snap.invite.clone()).width(Length::Fill).into());
            items.push(
                button(text("复制"))
                    .on_press(Message::CopyInvite)
                    .padding([8, 16])
                    .into(),
            );
        }
        if busy {
            items.push(
                button(text("离开房间"))
                    .on_press(Message::Leave)
                    .padding([8, 16])
                    .into(),
            );
        }
        items.push(text(ADVICE).width(Length::Fill).into());
        if self.proxy {
            items.push(text(DETECTED).width(Length::Fill).into());
            items.push(text(self.rules()).width(Length::Fill).into());
            items.push(
                button(text("复制规则"))
                    .on_press(Message::CopyRules)
                    .padding([8, 16])
                    .into(),
            );
        }
        items.push(
            button(text("修改服务器"))
                .on_press(Message::EditServer)
                .padding([8, 16])
                .into(),
        );
        push_notes(&mut items, self);
        column(items).spacing(12).into()
    }
}

impl Drop for App {
    fn drop(&mut self) {
        self.stop_session();
    }
}

fn push_notes<'a>(items: &mut Vec<Element<'a, Message>>, app: &'a App) {
    if !app.form_error.is_empty() {
        items.push(text(app.form_error.clone()).width(Length::Fill).into());
    }
    if !app.snap.notice.is_empty() {
        items.push(text(app.snap.notice.clone()).width(Length::Fill).into());
    }
    if !app.notice.is_empty() {
        items.push(text(app.notice.clone()).width(Length::Fill).into());
    }
    if !app.banner.is_empty() {
        items.push(text(app.banner.clone()).width(Length::Fill).into());
    }
}

fn info<'a>(label: &'a str, value: String) -> Element<'a, Message> {
    row![
        text(label).width(88).size(16),
        text(value).size(16).width(Length::Fill),
    ]
    .spacing(8)
    .into()
}

fn latency_text(ms: Option<u32>) -> String {
    match ms {
        Some(ms) => format!("{ms} ms"),
        None => "—".into(),
    }
}

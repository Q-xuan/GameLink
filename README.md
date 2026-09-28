# GameLink

GameLink 把若干玩家放进同一个虚拟子网 `10.66.0.0/24`，用一台中继转发 UDP。Linux 上的 Go 客户端只保持控制面和 UDP 会话。Windows 上的图形客户端会另外创建一块 Wintun 网卡，只路由这个子网。

房间保存在内存里，进程重启后清空。默认每个数据包都带房间令牌的截断 HMAC-SHA256。序列号只用于统计，不防重放。

服务器、房间和 UDP 中继仍是 Go。Windows 图形界面是 Rust 客户端，协议与 Go 头一致。旧的 `gamelink host` / `gamelink join` 命令仍然可用。

## 构建

使用已安装的 Go 1.22 或更新版本。

```bash
go test ./...
go build -o gamelink ./cmd/client
go build -o gamelink-server ./cmd/server
go build -o valheim-sim ./cmd/valheim-sim
```

静态 Linux amd64 服务器二进制：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o gamelink-server ./cmd/server
```

服务器切片版本仍是 `0.1.0`。Windows 图形客户端版本是 `0.3.0`。

## 运行服务器

控制面和 UDP 中继在同一个进程里。默认值与 `configs/server.example.env` 相同，可以用环境变量或同名参数覆盖。对外地址没有写死，部署时自己填。

```bash
go run ./cmd/server
```

常用参数：

```bash
go run ./cmd/server \
  --control-listen 127.0.0.1:41080 \
  --relay-listen 0.0.0.0:41000 \
  --public-control-url http://127.0.0.1:41080 \
  --public-relay 127.0.0.1:41000
```

本机关闭 MAC，方便联调：

```bash
go run ./cmd/server --insecure
```

进程不会终结 TLS，也不会监听 TCP 443。健康检查是 `GET /healthz`，成功时返回 `{"status":"ok"}`。

```bash
curl -sS http://127.0.0.1:41080/healthz
```

`POST /v1/rooms` 创建房间，返回短房间码、128 位十六进制令牌、peer 1、虚拟地址 `10.66.0.1`、对外中继地址和控制面 URL。`POST /v1/rooms/{code}/join` 携带令牌加入，按加入顺序分配 `10.66.0.N`，一个房间最多 8 人。加入后用 WebSocket `GET /v1/rooms/{code}/ws` 接收在线与离线通知。

## Go 命令行客户端

一个进程只加入一个房间。客户端先访问控制面，再向 UDP 中继握手，之后每 5 秒发送一次 Ping。20 秒没有 UDP 流量的成员会被移除。源端口变化时会重新绑定同一个 peer，虚拟地址不变。控制面来源地址和 UDP 来源地址不必相同。

```bash
gamelink host --control http://127.0.0.1:41080 --relay 127.0.0.1:41000
gamelink join CODE --token HEX --control http://127.0.0.1:41080 --relay 127.0.0.1:41000
```

未传 `--control` 或 `--relay` 时，命令行使用本机默认地址 `http://127.0.0.1:41080` 和 `127.0.0.1:41000`。创建房间接口返回的中继地址只在没有 `--relay` 时采用。两端都加 `--insecure` 才会跳过 MAC。图形客户端没有这个开关，始终校验 MAC。

## Windows 图形客户端

`client/` 是 Rust 2021 程序，界面用 iced。发布产物是一个 `gamelink.exe`。官方 Wintun 0.14.1 amd64 `wintun.dll` 嵌在这个 exe 里。启动会话前会核对内置文件的 SHA-256，再写到 exe 旁边，因为 Wintun 只能从那里加载。Release 附件本身不是 zip，也不带安装包。

双击时嵌入的清单要求管理员权限，Windows 会弹出 UAC。网卡名是 `GameLink`，地址是房间分配的 `10.66.0.N/24`，MTU 1280。只在这块网卡上添加 on-link 路由 `10.66.0.0/24`。不会安装默认路由，不会改 DNS，也不会改其他网卡。

第一次打开会用中文询问控制面 URL 和中继 `主机:端口`，并保存到当前用户的 AppData（`%APPDATA%\GameLink\settings.json`）。之后直接沿用。程序里不带服务器地址。

按钮是「创建房间」和「加入房间」。创建后给出邀请串，格式固定为：

```text
gamelink://join/<房间码>/<令牌>
```

旁边有「复制」。加入时粘贴这一整串。程序会为当前用户注册 `gamelink://` 协议。

窗口显示连接状态、虚拟地址、握手或 Ping 状态，以及延迟毫秒数。HTTP 超时不少于 10 秒。Ping 间隔 5 秒。握手在限定时间内完不成时，只提示一句：节点可能丢弃了 UDP。

直连是建议，不是要求。能转发 UDP 的节点（例如 Hysteria2、TUIC）也可以。如果发现 FlClash、Clash Verge 或 mihomo 的 TUN，窗口只提供可复制的规则，不改代理配置文件。IP 规则用本机已经保存的中继地址；还没有数字地址时，不会列出写死的 IP。

本地检查协议：

```bash
cd client && cargo test
```

GitHub Actions 在 `windows-latest` 上为标签 `v0.3.0` 发布单个 `gamelink.exe`。这里没有在 Windows 上点过界面，也没有加载过 Wintun。

界面字体是 Noto Sans CJK SC 的子集，许可证是 SIL Open Font License 1.1，见 `client/assets/OFL.txt`。Wintun 预编译库来自 <https://www.wintun.net/>，压缩包 SHA-256 是 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`，只嵌入其中 `wintun/bin/amd64/wintun.dll`。

## Valheim 流量模拟

不需要虚拟网卡。

```bash
valheim-sim host --port 2456
valheim-sim client --host 127.0.0.1 --port 2456 --duration 10s --pps 50
valheim-sim relay --count 20 --size 128
```

`host` 同时监听 `--port` 和下一端口（2456 与 2457），并回复 ACK。`client` 发送 SessionID、Sequence、Timestamp 和填充，包长 64 到 1200 字节，结束时打印 sent/recv、丢包、RTT 平均/p95/p99、抖动和吞吐。`relay` 在本机启动中继，让两个模拟端互相收发，供 `go test` 覆盖数据面。

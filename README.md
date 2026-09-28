# GameLink

GameLink 把若干玩家放进同一个虚拟子网 `10.66.0.0/24`，用一台中继转发 UDP。Linux 上的客户端只保持控制面和 UDP 会话。Windows 客户端会另外创建一块 Wintun 网卡，只路由这个子网。

房间保存在内存里，进程重启后清空。默认每个数据包都带房间令牌的截断 HMAC-SHA256。序列号只用于统计，不防重放。

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

服务器版本是 `0.1.0`，启动时会打印。服务器请在本地构建。Windows 图形客户端由 GitHub Actions 在 `windows-latest` 上编译，Release 附件是 `gamelink-windows-amd64.zip`。

## 运行服务器

控制面和 UDP 中继在同一个进程里。默认值与 `configs/server.example.env` 相同，可以用环境变量或同名参数覆盖。

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

## 客户端

一个进程只加入一个房间。客户端先访问控制面，再向 UDP 中继握手，之后每 5 秒发送一次 Ping。20 秒没有 UDP 流量的成员会被移除。源端口变化时会重新绑定同一个 peer，虚拟地址不变。

```bash
gamelink host --control http://127.0.0.1:41080 --relay 127.0.0.1:41000
gamelink join CODE --token HEX --control http://127.0.0.1:41080 --relay 127.0.0.1:41000
```

服务器若以默认配置启动，创建房间接口返回的中继地址是配置里的公网地址。连本机中继时请加上 `--relay 127.0.0.1:41000`，或让服务器使用 `--public-relay 127.0.0.1:41000`。两端都加 `--insecure` 才会跳过 MAC。

## Windows 客户端

双击 `gamelink.exe`。内嵌清单要求管理员权限，Windows 弹出 UAC，点允许即可，不必打开管理员终端。

同一个 exe 仍保留 `gamelink host` 和 `gamelink join`。窗口调用同一套房间、中继、Wintun 和路由代码。默认控制面是 `wss://gamelink.aruyx.com`（REST 走 `https://gamelink.aruyx.com`）。UDP 使用创建或加入房间时服务器返回的中继地址；已部署的中继是字面地址 `195.72.187.81:41000`。控制连接和 UDP 互不绑定来源地址。图形界面不会关闭 MAC。命令行联调本机时仍可使用已有的 `--insecure`，连这台公网服务器不要加。

创建房间后窗口给出一条邀请串，格式是 `gamelink://join/<房间码>/<令牌>`，旁边有「复制」。另一台电脑把这条串粘进「加入」，或直接点 `gamelink://` 链接。程序启动时为当前用户注册该协议，不需要单独的安装步骤。

窗口显示连接状态、本机虚拟地址，以及 Ping/Pong 往返延迟（毫秒）。握手过程会显示已等待的秒数。若在限定时间内没有握上，会显示一句：代理节点可能丢弃了 UDP。

建议直连，也可以使用能转发 UDP 的代理节点（例如 Hysteria2、TUIC）。走代理时节点必须转发 UDP，否则握手会停住。到 `gamelink.aruyx.com` 的 HTTPS 可以继续走代理。若检测到 FlClash、Clash Verge 或 mihomo 的 TUN（进程名 `FlClash.exe`、`clash-verge.exe`、`verge-mihomo.exe`、`mihomo.exe`，或本机地址落在 `198.18.0.0/15`），窗口会显示下面两条规则和「复制」，建议放在代理规则列表最前面。程序不修改代理配置、代理注册表或任何 Clash 文件。

```
PROCESS-NAME,gamelink.exe,DIRECT
IP-CIDR,195.72.187.81/32,DIRECT,no-resolve
```

路由不变：网卡名 `GameLink`，只添加 on-link 路由 `10.66.0.0/24`，MTU 1280。不安装默认路由，不改 DNS。退出时先删掉本进程加过的地址和这条路由，再关掉这块网卡。地址是房间分配的 `10.66.0.N/24`（主机是 `10.66.0.1/24`）。

官方 Wintun 0.14.1 amd64 的 `wintun.dll` 嵌在 exe 里。第一次运行时用 `os.Executable()` 定位目录，核对 DLL 的 SHA-256 后再写到 exe 旁边。不要换 0.13，也不要换非 amd64 的 DLL。不需要自己下载 Wintun。官方压缩包 SHA-256 是 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`。Go 绑定仍是 `golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2`。

从 Release 下载 `gamelink-windows-amd64.zip`，解压后双击。自行编译时，先用 `scripts/fetch-wintun.ps1`（或 `scripts/fetch-wintun.sh`）核对压缩包并放好 DLL，再执行：

```bat
go build -trimpath -o gamelink.exe ./cmd/client
```

这里没有在 Windows 上打开过这个窗口，也没有加载过 Wintun。

命令行：

```bat
gamelink.exe host
gamelink.exe join 房间码 --token 十六进制令牌
```

两台电脑都进同一房间之后，加入方执行 `ping 10.66.0.1`。

## Valheim 流量模拟

不需要虚拟网卡。

```bash
valheim-sim host --port 2456
valheim-sim client --host 127.0.0.1 --port 2456 --duration 10s --pps 50
valheim-sim relay --count 20 --size 128
```

`host` 同时监听 `--port` 和下一端口（2456 与 2457），并回复 ACK。`client` 发送 SessionID、Sequence、Timestamp 和填充，包长 64 到 1200 字节，结束时打印 sent/recv、丢包、RTT 平均/p95/p99、抖动和吞吐。`relay` 在本机启动中继，让两个模拟端互相收发，供 `go test` 覆盖数据面。

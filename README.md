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

当前切片版本是 `0.1.0`，启动时会打印。请在本地用上面的命令构建，本仓库不提供下载地址。

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

在 Windows 11 上，`gamelink host` 和 `gamelink join` 会创建名为 `GameLink` 的 Wintun 网卡，地址是房间分配的 `10.66.0.N/24`（主机是 `10.66.0.1/24`），MTU 1280。它只在这块网卡上添加 on-link 路由 `10.66.0.0/24`。不会安装 `0.0.0.0/0` 或 `::/0`，不会改 DNS，也不会删除其他网卡上的路由。退出时先删除本进程加过的地址和这条路由，再关掉这块网卡。

必须用管理员身份运行。没有提升权限时，程序会直接退出并提示中文错误。

本仓库不附带 `wintun.dll`，也不提供 `gamelink.exe` 的下载地址。在 Windows 上自行构建：

```bat
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o gamelink.exe ./cmd/client
```

官方签名驱动从 <https://www.wintun.net/> 下载 **Wintun 0.14.1**。压缩包 SHA-256 是 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`。解压后把 `wintun/bin/amd64/wintun.dll` 复制到 `gamelink.exe` 同一目录，文件名保持 `wintun.dll`。不要使用 x86、arm 或 arm64 那一份，也不要改放到 System32。Go 绑定是 `golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2`，对应 0.14 的 `WintunCreateAdapter(Name, TunnelType, RequestedGUID)`。0.13 的 DLL 不兼容。这里只在 Linux 上核对了 0.14.1 头文件和这份 Go 绑定，没有在 Windows 上加载过驱动。

默认连已部署的服务器，不要加 `--insecure`：

```bat
gamelink.exe host
gamelink.exe join 房间码 --token 十六进制令牌
```

控制面默认是 `wss://gamelink.aruyx.com`（REST 会走 `https://gamelink.aruyx.com`）。UDP 拨的是字面地址 `195.72.187.81:41000`，不是域名。控制连接和 UDP 互不绑定来源地址。

这台机器上如果还有别的代理 TUN（fake-ip，`gamelink.aruyx.com` 可能解析到 `198.18.0.0/15`），把 `gamelink.exe` 这个进程和 `195.72.187.81` 都设为 DIRECT。到 `gamelink.aruyx.com` 的 HTTPS 可以继续走代理。发往中继字面 IP 的 UDP 不能走代理。

第二个人还没加入时，可以先在一台电脑上确认：

- 网卡 `GameLink` 已经起来，地址是 `10.66.0.1/24`
- `route print` 里能看到 `10.66.0.0` 掩码 `255.255.255.0` 走这块网卡，默认路由没有变化
- 客户端日志里有 `正在握手 195.72.187.81:41000`，随后是 `握手完成，每 5 秒发送 Ping`

两台电脑都进同一房间之后，加入方执行 `ping 10.66.0.1`。

## Valheim 流量模拟

不需要虚拟网卡。

```bash
valheim-sim host --port 2456
valheim-sim client --host 127.0.0.1 --port 2456 --duration 10s --pps 50
valheim-sim relay --count 20 --size 128
```

`host` 同时监听 `--port` 和下一端口（2456 与 2457），并回复 ACK。`client` 发送 SessionID、Sequence、Timestamp 和填充，包长 64 到 1200 字节，结束时打印 sent/recv、丢包、RTT 平均/p95/p99、抖动和吞吐。`relay` 在本机启动中继，让两个模拟端互相收发，供 `go test` 覆盖数据面。

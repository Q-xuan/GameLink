# GameLink

GameLink 把若干玩家放进同一个虚拟子网 `10.66.0.0/24`，用一台中继转发 UDP。这一阶段只有房间控制面和 UDP 中继，没有虚拟网卡。

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

## Valheim 流量模拟

不需要虚拟网卡。

```bash
valheim-sim host --port 2456
valheim-sim client --host 127.0.0.1 --port 2456 --duration 10s --pps 50
valheim-sim relay --count 20 --size 128
```

`host` 同时监听 `--port` 和下一端口（2456 与 2457），并回复 ACK。`client` 发送 SessionID、Sequence、Timestamp 和填充，包长 64 到 1200 字节，结束时打印 sent/recv、丢包、RTT 平均/p95/p99、抖动和吞吐。`relay` 在本机启动中继，让两个模拟端互相收发，供 `go test` 覆盖数据面。

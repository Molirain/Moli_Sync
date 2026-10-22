# MoliSync Server（第一阶段：控制面骨架）

Debian 侧控制面服务，负责 mTLS 身份鉴别、设备在线表与多网卡候选池管理，并为后续的云同步、P2P 会话票据与流式中继提供基础。

- Go 1.22+，仅依赖标准库、`gorilla/websocket`、`gopkg.in/yaml.v3`
- 反代终止 mTLS，服务端只接受来自受信代理的身份 Header
- 设备唯一 ID 来自证书 SHA-256 指纹（或配置为证书 CN）
- 所有校验失败均 fail-closed，不信任 `X-Forwarded-For` 等可伪造头

协议定义见 [proto/sync.proto](../proto/sync.proto)。

## 目录结构

```text
server/
├── cmd/server/            # 进程入口、优雅关闭
├── internal/config/       # YAML + 环境变量配置、严格校验
├── internal/auth/         # 受信反代校验、证书身份解析、上下文注入
├── internal/registry/     # 并发安全设备表、候选端点校验
├── internal/ws/           # WebSocket 连接管理、心跳、在线状态广播
├── internal/api/          # HTTP 路由与中间件
├── config.example.yaml    # 带注释的完整配置示例
└── go.mod
```

## 构建与运行

```bash
cd server
go build -o bin/molisync-server ./cmd/server
sudo -u molisync ./bin/molisync-server -config /etc/molisync/config.yaml
```

```bash
go test ./...
go vet ./...
```

- `-config` 默认 `config.yaml`；显式指定但文件不存在会直接退出。
- 未显式指定且文件不存在时，使用内置默认值 + `MOLISYNC_*` 环境变量（容器场景）。
- `-version` 打印由 `-ldflags "-X main.version=..."` 注入的版本号。

## 配置

优先级：**内置默认值 → YAML → 环境变量**。YAML 使用严格模式，未知字段会导致启动失败；所有配置错误会一次性汇总报告。

完整字段与默认值见 [config.example.yaml](config.example.yaml)。关键项：

| 配置键 | 环境变量 | 说明 |
| --- | --- | --- |
| `server.listen` | `MOLISYNC_SERVER_LISTEN` | 监听地址，默认仅回环 `127.0.0.1:9520` |
| `proxy.trusted_cidrs` | `MOLISYNC_PROXY_TRUSTED_CIDRS` | 受信反代网段，逗号分隔；拒绝 `0.0.0.0/0`、`::/0` |
| `proxy.verify_header` | `MOLISYNC_PROXY_VERIFY_HEADER` | mTLS 校验结果头 |
| `proxy.fingerprint_header` | `MOLISYNC_PROXY_FINGERPRINT_HEADER` | 证书 SHA-256 指纹头 |
| `proxy.dn_header` | `MOLISYNC_PROXY_DN_HEADER` | 证书 Subject 头 |
| `proxy.required_verify_value` | `MOLISYNC_PROXY_REQUIRED_VERIFY_VALUE` | 通过值，默认 `SUCCESS` |
| `auth.device_id_source` | `MOLISYNC_AUTH_DEVICE_ID_SOURCE` | `fingerprint`（默认）或 `dn_cn` |
| `registry.offline_after` | `MOLISYNC_REGISTRY_OFFLINE_AFTER` | 无心跳判定离线时长，默认 `90s` |
| `websocket.allowed_origins` | `MOLISYNC_WEBSOCKET_ALLOWED_ORIGINS` | 浏览器 Origin 白名单，空表示拒绝所有浏览器来源 |

`device_id_source=fingerprint` 时 DeviceID 为小写 64 位十六进制指纹；`dn_cn` 时形如 `cn:手机-A`。两种命名空间不会冲突。

## 反向代理契约

代理必须完成 mTLS 链路与 CRL 校验，并且只把校验通过的身份信息转发给本服务：

```nginx
# /api/v1/ws 与普通 API 共用
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection $connection_upgrade;
proxy_set_header X-Client-Verify $ssl_client_verify;
proxy_set_header X-Client-Fingerprint $ssl_client_fingerprint;
proxy_set_header X-Client-DN $ssl_client_s_dn;
```

服务端要求：

1. TCP 对端必须落在 `proxy.trusted_cidrs` 内，否则返回 `403 untrusted_proxy`。
2. `verify` 头必须为配置的通过值，指纹必须是 64 位 SHA-256 十六进制串。
3. 身份头重复出现会被拒绝（防 Header 走私）；保险起见，代理应清除客户端自带的同名头。
4. `dn_cn` 模式支持 RFC 2253/4514（`CN=...,O=...`）与 OpenSSL 斜杠格式，正确处理转义逗号、十六进制转义与多值 RDN。
5. 服务端不使用 `X-Forwarded-For` 做任何安全判断。

Nginx 请使用 `map` 生成 `$connection_upgrade`，并保持 `proxy_request_buffering off` 以便后续中继阶段流式转发。

## HTTP 接口

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| GET | `/healthz` | 否 | 存活探针，返回 `{"status":"ok","time":...}` |
| GET | `/api/v1/me` | 是 | 返回当前证书身份（便于联调校验 Header） |
| GET | `/api/v1/devices` | 是 | 在线设备快照（含候选 IP 池） |
| GET | `/api/v1/ws` | 是 | WebSocket 控制面 |

鉴权失败返回 JSON：`{"error":{"code":"...","message":"..."}}`。

## WebSocket 控制面

所有帧为单个 UTF-8 JSON 对象，`type` 为判别字段；未知字段会被忽略以便向前兼容，未知 `type` 返回 `unsupported_message`。二进制帧返回 `unsupported_message`。

### 握手与设备注册

客户端连接后必须在 `websocket.hello_timeout` 内发送 `hello`：

```json
{
  "type": "hello",
  "device": {"name": "手机 A", "platform": "android"},
  "endpoints": [
    {"network": "lan", "ip": "192.168.1.20", "port": 9527, "priority": 100},
    {"network": "vpn", "ip": "100.64.0.8", "port": 9527, "priority": 50}
  ]
}
```

服务端返回 `welcome`（含协议版本、心跳参数与其他在线设备）：

```json
{
  "type": "welcome",
  "protocol_version": 1,
  "device_id": "9f2c...",
  "server_time": 1760000000000,
  "heartbeat_interval_ms": 25000,
  "pong_timeout_ms": 60000,
  "devices": []
}
```

同一 DeviceID 允许多条连接，按连接数引用计数，最后一条连接断开才广播离线。

### 心跳与候选池

```json
{"type": "heartbeat", "endpoints": [{"network": "vpn", "ip": "100.64.0.9", "port": 9527}]}
```

- `endpoints` 存在（包括空数组）时整体替换候选池；缺省时保留旧值。
- 返回 `{"type":"heartbeat_ack","server_time":...,"endpoints_changed":true}`。
- 候选池变化会向其他设备广播 `presence` + `"state":"updated"`。
- 服务端每 `ping_interval` 发送 WebSocket Ping；Pong 会刷新在线时间，因此即使客户端暂时不发应用心跳也不会被误判离线。在线表淘汰由 `registry.offline_after` 控制。

### 在线状态与探活

```json
{"type":"presence","state":"online","device":{"device_id":"...","name":"电脑 C","platform":"windows","endpoints":[],"connected_at":1760000000000,"last_seen":1760000000000,"connections":1}}
```

`state` 取值 `online`、`updated`、`offline`。客户端可发送 `{"type":"ping","client_time":...}`，服务端回 `pong`。

### 错误与关闭

```json
{"type":"error","code":"invalid_endpoints","message":"endpoint 0: IP address must not be loopback","fatal":false}
```

- 非致命错误（如非法端点、非法 JSON）不会断开连接，客户端可在 hello 期限内重试注册。
- 致命错误会先发送 `fatal:true` 的错误帧，再发送关闭帧。
- 关闭码：`1000` 正常、`1001` 服务关机、`1008` 速率超限、`1011` 内部错误、`1013` 设备数已满或客户端读取过慢。
- 单条消息受 `websocket.read_limit_bytes` 限制，消息速率受 `websocket.max_messages_per_second` 限制（突发额度为两倍）。

### 当前实现范围

第一阶段只实现 `hello`、`heartbeat`、`ping` 三类控制消息，以及在线状态广播。`publish_record`、`request_p2p`、`relay_request` 等消息已在 [proto/sync.proto](../proto/sync.proto) 中定义，服务端会对当前不支持的类型明确返回 `unsupported_message`，不会静默丢弃。

## 安全设计要点

- **受信代理强约束**：启动时强制至少一个 CIDR 且拒绝全网段；不满足则拒绝启动。
- **身份头完整性**：身份头必须单值、长度受限；指纹格式、CN 转义与字符集严格校验。
- **最小暴露面**：默认只监听回环；Origin 白名单默认空，浏览器来源一律拒绝，原生客户端（无 Origin）正常放行。
- **资源上限**：设备数、候选端点数量、单帧大小、消息速率、发送缓冲均有硬上限；慢消费者会被主动断开，避免内存放大。
- **无持久化依赖**：第一阶段设备表在内存中，进程重启后由客户端重新注册；SQLite、Blob 与中继属于后续里程碑，因此这里没有任何占位实现或 mock。

## 部署建议

- 使用独立的非 root 用户运行，配置文件权限 `0600`。
- 反向代理与本服务走回环或私有容器网络，防火墙禁止外部直接访问 `9520`。
- 若使用 Docker，注意反代容器的源地址通常不是 `127.0.0.1`，需把实际网段加入 `proxy.trusted_cidrs`（例如 `172.18.0.0/16`），不要使用 `0.0.0.0/0`。
- 修改证书吊销列表后重载代理即可；被吊销的客户端会在下一次握手或重连时被网关拒绝，控制面随即将该设备下线。

# MoliSync Server

Debian 侧控制面与协同服务：mTLS 身份鉴别、设备在线表、多网卡候选池管理，以及云同步、P2P 会话票据与流式中继。

- Go 1.22+，仅依赖标准库、`gorilla/websocket`、`gopkg.in/yaml.v3`
- 反代终止 mTLS，服务端只接受来自受信代理的身份 Header
- 设备唯一 ID 来自证书 SHA-256 指纹（或配置为证书 CN）
- 所有校验失败均 fail-closed，不信任 `X-Forwarded-For` 等可伪造头

协议定义见 [proto/sync.proto](../proto/sync.proto)。

## 目录结构

```text
server/
├── cmd/server/            # 进程入口、健康探测、优雅关闭
├── internal/config/       # YAML + 环境变量配置、严格校验
├── internal/auth/         # 受信反代校验、证书身份解析、上下文注入
├── internal/registry/     # 并发安全设备表、候选端点校验
├── internal/ws/           # WebSocket 连接管理、心跳、在线状态广播、P2P 信令
├── internal/p2p/          # 一次性会话票据与连接配对中继
├── internal/api/          # HTTP 路由与中间件
├── Dockerfile             # 多阶段构建，distroless nonroot 运行时
├── .dockerignore
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
- `-healthcheck-url` 对运行中的实例执行一次 `/healthz` 探测后退出，供无 shell 的容器镜像做健康检查。

## Docker

镜像由 [`.github/workflows/server-image.yml`](../.github/workflows/server-image.yml) 自动构建并推送到
`ghcr.io/molirain/molisync-server`（`latest`、版本标签、`sha-<commit>`），支持 `linux/amd64` 与
`linux/arm64`。运行时基于 distroless，以 `nonroot` 运行，不含 shell 和包管理器。

```bash
docker build -t molisync-server:dev .
docker run --rm -p 127.0.0.1:9520:9520 \
  -e MOLISYNC_PROXY_TRUSTED_CIDRS=127.0.0.1/32 \
  molisync-server:dev
```

仓库根目录的 [`docker-compose.example.yml`](../docker-compose.example.yml) 是推荐的部署模板：

- 服务端只加入私有网络 `molisync-internal`（`172.28.0.0/24`），不发布端口；
- `MOLISYNC_PROXY_TRUSTED_CIDRS` 与该子网一致，**只有反向代理可以加入该网络**，否则任何容器都能伪造身份头；
- `read_only`、`cap_drop: [ALL]`、`no-new-privileges`，并用镜像内置的 `-healthcheck-url` 做健康检查；
- 命名卷 `molisync-data` 挂载到 `/var/lib/molisync`，SQLite 数据库与 blob 都在卷内，容器重建不会丢记录。

镜像默认 `MOLISYNC_SERVER_LISTEN=0.0.0.0:9520`、`MOLISYNC_STORAGE_DATA_DIR=/var/lib/molisync`。
不要把这个端口发布到宿主机或公网。如需 YAML 配置，挂载到 `/config.yaml:ro` 并设置
`command: ["-config", "/config.yaml"]`；环境变量仍然优先于 YAML。

## 持久化存储

服务端把云记录写入 SQLite（`<data_dir>/molisync.db`，WAL 模式），blob 写入
`<data_dir>/blobs/`（或 `storage.blob_dir`）。上传先写 `.part`，校验大小后原子重命名；
数据库插入失败会回滚删除文件。

- 文字记录上限 64 KiB。
- 单个 blob 上限由 `storage.max_item_bytes` 控制，默认 64 MiB。
- `GET /api/v1/blobs/{id}` 使用 `http.ServeContent`，原生支持 `Range` 断点续传、`206` 与 `416`。
- 记录永不自动过期；删除只能由发送者本人执行。
- 备份时同时备份 SQLite 数据库与 blob 目录（或整个卷）。


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
| `storage.data_dir` | `MOLISYNC_STORAGE_DATA_DIR` | SQLite 与默认 blob 根目录，默认 `./data`；容器内为 `/var/lib/molisync` |
| `storage.blob_dir` | `MOLISYNC_STORAGE_BLOB_DIR` | blob 目录，空表示 `<data_dir>/blobs` |
| `storage.max_item_bytes` | `MOLISYNC_STORAGE_MAX_ITEM_BYTES` | 单个 blob 上限，默认 `67108864`（64 MiB） |
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

Nginx 请使用 `map` 生成 `$connection_upgrade`，并保持 `proxy_request_buffering off` 以便中继阶段流式转发。中继端点需要支持连接升级后的双向裸字节转发（`proxy_http_version 1.1`、关闭响应缓冲）。

## HTTP 接口

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| GET | `/healthz` | 否 | 存活探针，返回 `{"status":"ok","time":...}` |
| GET | `/api/v1/me` | 是 | 返回当前证书身份（便于联调校验 Header） |
| GET | `/api/v1/devices` | 是 | 在线设备快照（含候选 IP 池） |
| GET | `/api/v1/ws` | 是 | WebSocket 控制面 |
| GET | `/api/v1/records` | 是 | 云记录列表，支持 `kind=text|image|file`、`limit`、`before` |
| POST | `/api/v1/records/text` | 是 | 保存文字记录并广播，body 为 `{"text":"..."}` |
| POST | `/api/v1/records/blob` | 是 | 流式上传 blob，查询参数 `kind=image|file`、`name`、`mime`，可选 `X-Blake3` |
| GET | `/api/v1/blobs/{id}` | 是 | 下载 blob，支持 HTTP Range 断点续传 |
| DELETE | `/api/v1/records/{id}` | 是 | 删除自己的记录与 blob |

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

控制面已实现 `hello`、`heartbeat`、`ping`、`publish_record`（文字）以及在线状态广播。
`publish_record` 会把文字写入 SQLite，并向其他在线设备广播 `sync_record`；发送者收到
`record_ack`。图片与文件通过 HTTP API 上传，服务端写入 blob 后同样广播 `sync_record`
元数据。

P2P 信令：客户端发送 `open_session`（`to_device_id`，可选 `transfer_name`），服务端生成
一次性 32 字节票据与 `session_id`（TTL 2 分钟），把同一份 `session_ticket` 帧推给双方，
其中包含双方的设备信息、候选地址、中继地址与过期时间。目标离线时返回 `peer_unavailable`。
票据只存在于进程内存，2 分钟后或会话结束后被清除。

中继：`GET /api/v1/relay/{session_id}` 携带 `X-Session-Ticket`。服务端校验调用者必须是会话
参与方，劫持连接后写入 `200 OK`，等待另一方到达（15 秒超时），随后用 `io.Copy` 双向转发，
字节流不落盘。中继不做任何明文改写，端到端保护由两端证书与票据校验保证。

`relay_request` 消息仍保留在协议中用于兼容，但当前实现使用 HTTP 中继端点。

### 统计与升级提示

- `GET /api/v1/stats`（需设备身份）返回本部署的设备总数、当前在线、7/30 天活跃、平台分布、记录数与字节数，以及 `server_update` 与 `telemetry` 状态。
- `GET /api/v1/me` 额外返回 `server_version` 与 `server_update`；客户端据此提示“服务端需要升级”，无需自己查询 GitHub。
- 设备活跃度在 `hello` 时写入 `devices` 表，标识是 `HMAC-SHA256(每部署随机盐, 证书指纹)`，数据库里没有可逆指纹。
- `telemetry` 段控制匿名聚合上报：默认 `endpoint` 为空即关闭；设置后端每 24 小时 POST 一次聚合计数，不含设备标识、名称、IP 或记录内容。
- 收集器的 `installations` 是**单调递增的安装总数**（每个部署按唯一设备计一次，取历史最大值求和）；`install_events` 只按天记录新增数量，不存设备标识。`GET /v1/stats?format=text` 输出适合终端阅读的摘要。
- `update_check` 段控制服务端自检，每 12 小时读一次 `server-vX.Y.Z` release；`version` 为 `dev` 的开发构建会自动跳过。

发布使用分端 tag：`server-vX.Y.Z` 只构建服务端与收集器镜像，`desktop-vX.Y.Z` 与 `android-vX.Y.Z` 互不影响。

## 安全设计要点

- **受信代理强约束**：启动时强制至少一个 CIDR 且拒绝全网段；不满足则拒绝启动。
- **身份头完整性**：身份头必须单值、长度受限；指纹格式、CN 转义与字符集严格校验。
- **最小暴露面**：默认只监听回环；Origin 白名单默认空，浏览器来源一律拒绝，原生客户端（无 Origin）正常放行。
- **资源上限**：设备数、候选端点数量、单帧大小、消息速率、发送缓冲、中继并发均有硬上限；慢消费者会被主动断开，避免内存放大。
- **持久化边界**：设备表与会话票据只在内存中，进程重启后由客户端重新注册；SQLite 与 blob 目录持久化记录，Docker 部署下挂载在 `molisync-data` 卷。

## 部署建议

- 使用独立的非 root 用户运行，配置文件权限 `0600`。
- 反向代理与本服务走回环或私有容器网络，防火墙禁止外部直接访问 `9520`。
- 若使用 Docker，注意反代容器的源地址通常不是 `127.0.0.1`，需把实际网段加入 `proxy.trusted_cidrs`（例如 `172.18.0.0/16`），不要使用 `0.0.0.0/0`。
- 修改证书吊销列表后重载代理即可；被吊销的客户端会在下一次握手或重连时被网关拒绝，控制面随即将该设备下线。

# MoliSync

<img src="assets/branding/molisync-icon-512.png" width="96" alt="MoliSync 图标">

**安全优先的自托管跨端文件传输与同步项目 —— 围绕 mTLS 身份与流式 I/O 设计。**

简体中文 · [English](README.md)

MoliSync 是一个开源项目，用于在你自己掌控的设备之间传输文件、同步数据。仓库目前包含 Go 控制面服务、Windows .NET 客户端和 Android 客户端。各组件仍在开发中；端到端能力现状见[实现状态](#实现状态)。

## 仓库结构

| 路径 | 组件 |
| --- | --- |
| [`server/`](server/) | Go 控制面服务：受信反代鉴权、设备在线表、WebSocket 在线状态与 HTTP API，并提供容器镜像。 |
| [`client-windows/`](client-windows/) | C# / .NET 8 + Avalonia Windows 桌面客户端：自动读取系统证书、三栏云同步、剪贴板/占位处理、Range 下载、图片缩略图与查看器、在线设备与 IP 详情、P2P 收发、数据目录迁移与开机自启。 |
| [`client-android/`](client-android/) | Kotlin / Jetpack Compose Android 客户端：KeyChain mTLS、系统分享、流式发送、P2P 收发与前台数据同步服务、MediaStore 相册归档。 |
| [`proto/sync.proto`](proto/sync.proto) | 带版本的控制面与数据面协议定义。 |
| [`docker-compose.example.yml`](docker-compose.example.yml) | 服务端容器加固后的 Compose 模板。 |
| [`assets/branding/`](assets/branding/) | 统一图标源文件、PNG 渲染与可复现的生成脚本。 |
| [`.github/workflows/`](.github/workflows/) | Android 构建/发布与服务端容器镜像流水线。 |
| [架构白皮书](MoliSync%20分布式跨端同步与点对点流式互传系统技术架构白皮书.md) | 原始架构与部署设计说明。 |

## 架构

```text
 Windows 客户端 ─┐                      ┌─ Android 客户端
                 ├── HTTPS / WebSocket ─┤
                 │   由反代终止 mTLS     │
                 └──────┬───────────────┘
                        │ 已验证的身份头
                  受信反向代理
                        │ 私有 Docker 网络 / 回环
                  Go 控制面 + 持久化存储
                  在线设备 · 记录 · blob

      数据面：先并发探测 LAN/VPN 候选地址直连 P2P，
              1.2 秒内无果则回退服务端加密中继
```

- **控制面：**客户端使用客户端证书连接反向代理；代理执行 mTLS 校验，并向 Go 服务透传校验结果、证书指纹和 Subject。
- **设备身份：**默认以客户端证书的 SHA-256 小写指纹作为稳定的 `DeviceID`。
- **反向代理信任边界：**服务端只信任配置白名单内的代理 CIDR。代理必须先清除客户端自带的身份头，再写入自行验证的头。
- **数据面：**服务端签发一次性 32 字节会话票据并推送给双方；两端用系统证书完成双向 TLS 后按 49 字节定长帧流式传输，P2P 不可达时服务端以 `io.Copy` 零落盘中继同一会话。
- **云端持久化：**文字（≤64 KiB）存入 SQLite；图片与文件流式写入 blob 目录，下载支持 HTTP Range。记录不自动过期，且只有发送者能删除。Docker 部署下两者都在 `molisync-data` 卷内。

## 同步规则

| 内容 | 行为 |
| --- | --- |
| 文字 | ≤64 KiB，始终保存在服务器，所有已授权设备可见，一键复制；客户端长文本可展开全文并选区复制 |
| 图片/文件 ≤ 阈值 | 每次由客户端询问：存服务器，或 P2P 直连 |
| 图片/文件 > 阈值 | 只能 P2P |
| 默认阈值 | 15 MiB，Android 客户端可在 5–50 MiB 之间调整 |
| 服务端保留 | 永久，无自动清理、暂无配额 |
| 下载 | `GET /api/v1/blobs/{id}`，支持 Range 断点续传 |
| P2P 直连 | 客户端广告 LAN/VPN 候选地址，收方并发探测（300 ms 阶梯），1.2 秒内不通则回退中继 |
| P2P 中继 | 服务端只做透明字节流转发，端到端加密由两端自行保证 |
| Android 归档 | 图片进入相册 `Pictures/MoliSync`，文件进入 `Download/MoliSync` |

Android 客户端用“文字 / 图片 / 文件”三栏实现这套规则。服务端提供一组精简 HTTP API 与 WebSocket 通知：

| 端点 | 作用 |
| --- | --- |
| `GET /api/v1/records` | 拉取记录（`kind`、`limit`、`before`） |
| `POST /api/v1/records/text` | 保存文字记录并广播 |
| `POST /api/v1/records/blob` | 流式上传图片/文件（`kind`、`name`、`mime`，可选 `X-Blake3`） |
| `GET /api/v1/blobs/{id}` | 下载 blob，支持 Range |
| `DELETE /api/v1/records/{id}` | 删除自己的记录与 blob |
| `GET /api/v1/relay/{session_id}` | 中继字节流（`X-Session-Ticket`，连接劫持后双向转发） |

## 实现状态

| 能力 | 状态 |
| --- | --- |
| Go 服务端配置、受信代理 mTLS 身份、设备表、WebSocket 在线状态与 API | 已实现 |
| 云端持久化：SQLite 元数据、blob 目录、Range 下载、仅发送者可删除、Docker 卷 | 已实现 |
| 同步记录、P2P 票据、中继与数据帧的协议定义 | 已实现（`proto/sync.proto` 与 JSON 线上格式一致） |
| P2P 信令：一次性 32 字节会话票据、在线设备候选地址下发、中继地址 | 已实现 |
| P2P 中继：连接配对 + `io.Copy` 零落盘双向转发、15 秒配对超时 | 已实现 |
| Windows P2P 监听/发送、Happy Eyeballs 并发探测、中继回退、在线设备与 IP 详情 | 已实现 |
| Android P2P 监听/发送、中继回退、MediaStore 归档、设备名称、长按操作 | 已实现 |
| Windows Avalonia 桌面界面、自动读取系统证书、三栏云同步、图片缩略图与查看器、Range 下载、数据目录迁移与开机自启 | 已实现 |
| Android KeyChain mTLS、三栏云界面、流式 HTTP 上传、Range 下载、系统分享、基于 PFD 的发送、BLAKE3 分帧与前台服务 | 已实现 |
| 端到端 P2P 同步、票据交换、可互操作的对端监听、中继 | 已实现（两端均已接入，仍需真实网络回归） |
| 客户端自动检查更新 | 暂缓 |

三端的文字同步走服务器；阈值内的图片/文件可存入服务器并由任意授权设备下载，也可以选择直接 P2P。P2P 不可达时自动回退服务端中继，端侧仍持有会话票据做校验。Windows 与 Android 都支持自定义设备名、查看在线设备及其 LAN/VPN 地址。

## Docker 快速开始

服务端镜像发布在 GitHub Container Registry：

```sh
docker pull ghcr.io/molirain/molisync-server:latest
```

标签：`latest`（默认分支）、发布标签对应的 `vX.Y.Z` 与 `X.Y`，以及每次发布构建的 `sha-<commit>`。镜像支持多架构（`linux/amd64`、`linux/arm64`），以 `nonroot` 运行，且不包含 shell 或包管理器。

从示例 Compose 文件开始：

```sh
cp docker-compose.example.yml docker-compose.yml
docker compose up -d
docker compose ps
```

Compose 文件只启动控制面，接入私有网络 `molisync-internal`（子网 `172.28.0.0/24`），且**不会**把 9520 端口发布到宿主机。你现有的反向代理（Nginx、NPM、Caddy 等）必须加入该网络，终止 mTLS、校验证书、清除入站的 `X-Client-*` 头，再写入新的已验证值：

```nginx
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection $connection_upgrade;
proxy_request_buffering off;
proxy_buffering off;

proxy_set_header X-Client-Verify $ssl_client_verify;
proxy_set_header X-Client-Fingerprint $ssl_client_fingerprint;
proxy_set_header X-Client-DN $ssl_client_s_dn;
```

关键容器配置（均可通过环境变量传入；完整列表见 [`server/config.example.yaml`](server/config.example.yaml)）：

| 环境变量 | 作用 |
| --- | --- |
| `MOLISYNC_SERVER_LISTEN` | 容器内监听地址。镜像默认 `0.0.0.0:9520`；不要把它发布到公网。 |
| `MOLISYNC_PROXY_TRUSTED_CIDRS` | 允许断言身份的代理网段。必须与 Compose 子网一致，且只有反向代理可以加入该网络。 |
| `MOLISYNC_PROXY_VERIFY_HEADER` / `..._FINGERPRINT_HEADER` / `..._DN_HEADER` | 代理透传校验结果、证书指纹与 Subject 的头名称。 |
| `MOLISYNC_AUTH_DEVICE_ID_SOURCE` | `fingerprint`（推荐）或 `dn_cn`。 |
| `MOLISYNC_STORAGE_DATA_DIR` | SQLite 与默认 blob 根目录。镜像内为 `/var/lib/molisync`，由 `molisync-data` 卷支撑。 |
| `MOLISYNC_STORAGE_MAX_ITEM_BYTES` | 单个上传 blob 上限，默认 64 MiB。 |
| `MOLISYNC_LOG_LEVEL` / `MOLISYNC_LOG_FORMAT` | `debug`/`info`/`warn`/`error` 与 `json`/`text`。 |

注意：

- **任何能访问该受信网络的容器都可以伪造身份头。** 请确保 `molisync-internal` 中只有反向代理与服务端。
- 若要用 YAML 配置文件替代环境变量，请只读挂载并传入 `-config`：`./server/config.yaml:/config.yaml:ro`，并设置 `command: ["-config", "/config.yaml"]`。环境变量仍然优先于文件。
- 镜像健康检查通过二进制自身的 `-healthcheck-url` 探测 `http://127.0.0.1:9520/healthz`。若修改监听端口，请同时覆盖 Compose 健康检查。
- 私有 GHCR 包需要先 `docker login ghcr.io`，或在包设置中改为公开后才能拉取。
- 记录与 blob 保存在命名卷 `molisync-data` 中，容器重建不会丢失。请备份该卷（至少备份 SQLite 数据库与 `blobs/` 目录）；删除卷即删除全部同步内容。Compose 对容器设置了 `read_only: true`，所有写入都只落在卷内。

本地构建镜像：

```sh
cd server
docker build -t molisync-server:dev .
docker run --rm -p 127.0.0.1:9520:9520 \
  -e MOLISYNC_PROXY_TRUSTED_CIDRS=127.0.0.1/32 \
  molisync-server:dev
```

## 安全模型

- 不要信任客户端自行提交的身份头。严格设置 `proxy.trusted_cidrs`，服务端只绑定回环或私有网络。
- 反向代理应验证客户端证书（按部署需要启用吊销校验），删除入站同名身份头，并重新写入验证后的值。
- 若依赖代理完成身份鉴权，不要将 Go 服务直接暴露到公网。
- 示例 Compose 中容器以 `nonroot` 运行，根文件系统只读，丢弃全部 capabilities 并启用 `no-new-privileges`。
- Android 和 Windows 客户端使用操作系统证书/私钥存储；客户端私钥不会导出到应用文件。
- Android 强制 HTTPS、禁用明文流量和 mTLS HTTP 重定向；放入 `molisync_ca.crt` 后会使用该私有 CA 校验服务端。
- 对外部署前请阅读各平台说明并审查配置。本项目仍在开发阶段，尚不是完整的生产级同步/中继系统。

## 环境要求

- **服务端：**Debian/Linux、Go 1.22 或更高版本；或使用 Docker 24+ 运行容器镜像。
- **Windows：**Windows 10/11、.NET 8 SDK；NativeAOT 发布还需要 Visual C++ 构建工具。
- **Android：**Android 8.0 / API 26 或更高版本、JDK 17+、Android SDK Platform 35 和 Build-Tools 35.0.0。

## 构建与测试

### 服务端

```sh
cd server
go test ./...
go vet ./...
go build -o bin/molisync-server ./cmd/server
```

以 [`server/config.example.yaml`](server/config.example.yaml) 为配置起点。服务端支持 YAML 与 `MOLISYNC_*` 环境变量；运行前必须配置可信代理 CIDR 和身份透传头名称。

### Windows 客户端

```powershell
cd client-windows
dotnet build MoliSync.Client.sln -c Release
dotnet test MoliSync.Client.sln -c Release
dotnet publish src/MoliSync.Client/MoliSync.Client.csproj -c Release -r win-x64 -o publish
```

运行 `publish\MoliSync.Client.exe`，首次启动只需填写 HTTPS 服务地址；客户端证书会自动从 Windows 个人证书库读取。若服务端使用私有 CA，请把该 CA 一次性导入 Windows“受信任的根证书颁发机构”。详见 [Windows 客户端说明](client-windows/README.md)。

### Android 客户端

```sh
cd client-android
./gradlew :app:assembleDebug
./gradlew :app:testDebugUnitTest
./gradlew :app:lintDebug
```

Windows 上使用 `gradlew.bat`。KeyChain 设置、私有 CA、系统分享和 Release 签名说明见 [Android 客户端文档](client-android/README.md)。

## 持续集成与发布

| 工作流 | 触发条件 | 产物 |
| --- | --- | --- |
| [`android.yml`](.github/workflows/android.yml) | PR；推送到 `main`/`master`；`v*` 标签 | 构建/测试/Lint；版本标签时把签名 APK 附加到 GitHub Release |
| [`server-image.yml`](.github/workflows/server-image.yml) | `server/` 下的改动；`v*` 标签 | `go vet` + `go test -race`，随后构建多架构镜像并推送到 GHCR |

Android 发布签名需要以下仓库 secrets：`ANDROID_KEYSTORE_BASE64`、`ANDROID_KEYSTORE_PASSWORD`、`ANDROID_KEY_ALIAS`、`ANDROID_KEY_PASSWORD`。准备步骤见 Android 文档。不要把发布密钥库或密码提交到仓库。

## 文档

- [服务端配置与协议行为](server/README.md)
- [Windows 客户端](client-windows/README.md)
- [Android 客户端](client-android/README.md)
- [协议定义](proto/sync.proto)
- [架构白皮书](MoliSync%20分布式跨端同步与点对点流式互传系统技术架构白皮书.md)

Android 启动器/通知图标、Windows 可执行文件图标与本页标识使用同一套“同步 + 文件”图形。服务端只提供 HTTP/WebSocket API，没有可打标的图形界面；[`assets/branding/`](assets/branding/) 保留共享源文件供后续 UI 使用。

## 许可证

仓库目前尚未声明统一许可证。在添加许可证之前，请勿默认源代码可以再分发或复用。

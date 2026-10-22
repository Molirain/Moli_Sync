# MoliSync

**Secure, self-hosted cross-device file transfer and sync — built around mTLS identity and streaming I/O.**

MoliSync is an open-source project for moving files and synchronized data between devices you control. The repository currently contains a Go control-plane foundation, a Windows .NET client, and an Android client. The components are under active development; see [Implementation status](#implementation-status) for what is and is not wired end to end.

**安全优先的自托管跨端文件传输与同步项目。** MoliSync 面向用户自有设备，围绕 mTLS 身份、流式 I/O 和可自托管服务端设计。仓库目前包含 Go 控制面基础、Windows .NET 客户端和 Android 客户端。项目仍在开发中；端到端能力现状见[实现状态](#实现状态)。

## Repository layout / 仓库结构

| Path | Component |
| --- | --- |
| [`server/`](server/) | Go control-plane service: trusted reverse-proxy authentication, device registry, WebSocket presence and HTTP API. |
| [`client-windows/`](client-windows/) | C# / .NET 8 Windows client: Windows certificate-store access, control-plane WebSocket, clipboard/placeholder handling, Range download and connection racing. |
| [`client-android/`](client-android/) | Kotlin / Jetpack Compose Android client: KeyChain mTLS, system share intents, streaming sender and foreground data-sync service. |
| [`proto/sync.proto`](proto/sync.proto) | Versioned control-plane and data-plane protocol definition. |
| [Architecture whitepaper](MoliSync%20分布式跨端同步与点对点流式互传系统技术架构白皮书.md) | Original architecture and deployment design notes (Chinese). |

## Architecture / 架构

```text
 Windows client ─┐                       ┌─ Android client
                 ├── HTTPS / WebSocket ──┤
                 │   mTLS at proxy       │
                 └──────┬────────────────┘
                        │ authenticated identity headers
                 Trusted reverse proxy
                        │ private network / loopback
                 Go control plane
                 device presence + endpoints

          Planned data plane: direct P2P, then encrypted relay
```

- **Control plane:** clients authenticate to a reverse proxy using client certificates. The proxy validates mTLS and forwards the verification result, certificate fingerprint, and subject to the Go service.
- **Device identity:** by default, the server uses the lowercase SHA-256 fingerprint of the client certificate as the stable `DeviceID`.
- **Proxy trust boundary:** the server accepts identity headers only from configured trusted proxy CIDRs. The proxy must remove client-supplied copies of those headers before setting its own values.
- **Data plane:** the protocol defines framed streaming data. Direct P2P coordination, session-ticket issuance, and relay are planned; they are not yet implemented end to end by the server.

- **控制面：**客户端使用客户端证书连接反向代理；代理执行 mTLS 校验，并向 Go 服务透传校验结果、证书指纹和 Subject。
- **设备身份：**默认以客户端证书的 SHA-256 小写指纹作为稳定的 `DeviceID`。
- **反向代理信任边界：**服务端只信任配置白名单内的代理 CIDR。代理必须先清除客户端自带的身份头，再写入自行验证的头。
- **数据面：**协议已定义分帧流式数据格式；P2P 信令、会话票据及中继仍属于计划功能，服务端尚未端到端实现。

## Implementation status / 实现状态

| Capability | Status |
| --- | --- |
| Go server configuration, trusted-proxy mTLS identity, device registry, WebSocket presence and API | Implemented foundation |
| Protocol definition for sync records, P2P tickets, relay and data frames | Defined; server handlers are incomplete |
| Windows certificate selection, control-plane client, clipboard/placeholder handling, Range download and LAN/VPN race connector | Client modules implemented; server-side sync/P2P integration remains incomplete |
| Android KeyChain mTLS, share intents, PFD-based streaming sender, BLAKE3 framing and foreground service | Client modules implemented; requires a compatible receiving peer and P2P signaling |
| End-to-end sync, P2P ticket exchange, interoperable peer listener, relay and complete download flow | Not yet complete |

The Android client currently sends shared files; it does not yet implement a receiver or clipboard text synchronization. Its P2P connector needs server signaling and a peer listener before direct transfer can work. The server currently supports device registration, heartbeat, ping and presence; protocol messages for record publishing, P2P offers and relay are not all handled.

Android 客户端目前支持“分享并发送”文件，尚未实现接收端或剪贴板文本同步。P2P 连接器还需要服务端信令与对端监听才能完成直连。服务端目前实现设备注册、心跳、ping 和在线状态广播；记录发布、P2P offer、中继等协议消息尚未全部接入。

## Security model / 安全模型

- Never trust client-provided identity headers. Configure `proxy.trusted_cidrs` narrowly and bind the service to loopback or a private interface.
- Configure the reverse proxy to verify client certificates (including revocation policy where applicable), strip inbound identity headers, and set fresh verified values.
- Do not expose the Go service directly to the public internet when relying on proxy-authenticated identity.
- Android and Windows clients use OS-managed certificate/private-key stores; client private keys are not exported to application files.
- Android requires HTTPS, disables cleartext traffic and HTTP redirects for the mTLS client, and uses a pinned private CA when `molisync_ca.crt` is packaged.
- Review each platform README and deployment configuration before exposing a service. This project is under active development and is not yet a complete production sync/relay system.

- 不要信任客户端自行提交的身份头。严格设置 `proxy.trusted_cidrs`，服务端只绑定回环或私有网卡。
- 反向代理应验证客户端证书（按部署需要启用吊销校验），删除入站同名身份头，并重新写入验证后的值。
- 若依赖代理完成身份鉴权，不要将 Go 服务直接暴露到公网。
- Android 和 Windows 客户端使用操作系统证书/私钥存储；客户端私钥不会导出到应用文件。
- Android 强制 HTTPS、禁用明文流量和 mTLS HTTP 重定向；放入 `molisync_ca.crt` 后会使用该私有 CA 校验服务端。
- 对外部署前请阅读各平台说明并审查配置。本项目仍在开发阶段，尚不是完整的生产级同步/中继系统。

## Requirements / 环境要求

- **Server:** Debian/Linux, Go 1.22 or newer.
- **Windows:** Windows 10/11, .NET 8 SDK. NativeAOT publishing additionally requires the Visual C++ build tools.
- **Android:** Android 8.0 / API 26 or newer, JDK 17+, Android SDK Platform 35 and Build-Tools 35.0.0.

- **服务端：**Debian/Linux、Go 1.22 或更高版本。
- **Windows：**Windows 10/11、.NET 8 SDK；NativeAOT 发布还需要 Visual C++ 构建工具。
- **Android：**Android 8.0 / API 26 或更高版本、JDK 17+、Android SDK Platform 35 和 Build-Tools 35.0.0。

## Build and test / 构建与测试

### Server / 服务端

```sh
cd server
go test ./...
go vet ./...
go build -o bin/molisync-server ./cmd/server
```

Use [`server/config.example.yaml`](server/config.example.yaml) as the starting point. The service supports YAML plus `MOLISYNC_*` environment variables; configure trusted proxy CIDRs and proxy header names before running it.

以 [`server/config.example.yaml`](server/config.example.yaml) 为配置起点。服务端支持 YAML 与 `MOLISYNC_*` 环境变量；运行前必须配置可信代理 CIDR 和身份透传头名称。

### Windows client / Windows 客户端

```powershell
cd client-windows
dotnet build MoliSync.Client.sln -c Release
dotnet test MoliSync.Client.sln -c Release
dotnet publish src/MoliSync.Client/MoliSync.Client.csproj -c Release -r win-x64 -o publish
```

Copy [`client.example.json`](client-windows/client.example.json) to `client.json`, then configure the HTTPS server URL and CA certificate path. See the [Windows client guide](client-windows/README.md).

将 [`client.example.json`](client-windows/client.example.json) 复制为 `client.json`，配置 HTTPS 服务地址和 CA 证书路径。详见 [Windows 客户端说明](client-windows/README.md)。

### Android client / Android 客户端

```sh
cd client-android
./gradlew :app:assembleDebug
./gradlew :app:testDebugUnitTest
./gradlew :app:lintDebug
```

On Windows, use `gradlew.bat`. See the [Android client guide](client-android/README.md) for KeyChain setup, private CA configuration, share handling and release signing.

Windows 上使用 `gradlew.bat`。KeyChain 设置、私有 CA、系统分享和 Release 签名说明见 [Android 客户端文档](client-android/README.md)。

## Continuous integration and releases / 持续集成与发布

The GitHub Actions workflow at [`.github/workflows/android.yml`](.github/workflows/android.yml) builds, tests and lints Android on pull requests and pushes to `main`/`master`. A signed APK release is created when a `v*` version tag is pushed, provided the repository Actions secrets are configured:

- `ANDROID_KEYSTORE_BASE64`
- `ANDROID_KEYSTORE_PASSWORD`
- `ANDROID_KEY_ALIAS`
- `ANDROID_KEY_PASSWORD`

See the Android guide for preparing these secrets. Never commit the release keystore or its passwords.

[`.github/workflows/android.yml`](.github/workflows/android.yml) 会在 Pull Request 及推送到 `main`/`master` 时构建、测试并检查 Android。推送 `v*` 版本标签后，若仓库已配置 Actions secrets，则会签名并创建 APK Release：

- `ANDROID_KEYSTORE_BASE64`
- `ANDROID_KEYSTORE_PASSWORD`
- `ANDROID_KEY_ALIAS`
- `ANDROID_KEY_PASSWORD`

Secrets 的准备步骤见 Android 文档。不要把发布密钥库或密码提交到仓库。

## Documentation / 文档

- [Server setup and protocol behavior](server/README.md) / [服务端配置与协议行为](server/README.md)
- [Windows client](client-windows/README.md) / [Windows 客户端](client-windows/README.md)
- [Android client](client-android/README.md) / [Android 客户端](client-android/README.md)
- [Protocol](proto/sync.proto) / [协议定义](proto/sync.proto)
- [Architecture whitepaper (Chinese)](MoliSync%20分布式跨端同步与点对点流式互传系统技术架构白皮书.md) / [架构白皮书](MoliSync%20分布式跨端同步与点对点流式互传系统技术架构白皮书.md)

## License / 许可证

No repository-wide license has been declared yet. Until a license is added, do not assume that source code may be redistributed or reused.

仓库目前尚未声明统一许可证。在添加许可证之前，请勿默认源代码可以再分发或复用。

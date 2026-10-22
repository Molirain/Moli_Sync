# MoliSync Android 客户端

Kotlin + Jetpack Compose 客户端，面向 Android 8.0（API 26）及以上，`targetSdk 35`。
本模块聚焦四件事：

1. **系统 KeyChain mTLS**：证书与私钥由 Android 系统托管，应用只保存证书别名。
2. **三栏云同步**：文字、图片、文件三个标签页共享服务端持久化记录；文字一律上传服务器，
   图片/文件在阈值内可每次选择云端或 P2P。
3. **系统分享零拷贝发送**：接管 `ACTION_SEND` / `ACTION_SEND_MULTIPLE`，直接流式读取
   `content://` 句柄，不把文件复制到应用缓存。
4. **前台数据同步服务**：大文件传输由 `dataSync` 类型的前台服务接管，通知栏实时展示
   速率/进度，并适配 Android 14 权限与 Android 15 `onTimeout`。
5. **P2P 收发与中继回退**：应用内常驻 TLS 监听，配合服务端会话票据完成直连；不通时自动走中继。
6. **相册归档与设备管理**：下载的图片进入 `Pictures/MoliSync`、文件进入 `Download/MoliSync`；
   可自定义设备名，点开在线设备可查看全部 LAN/VPN 地址。

## 构建

要求：

- JDK 17 或更高（本项目使用 JDK 21 验证）
- Android SDK Platform 35、Build-Tools 35.0.0
- 通过 `ANDROID_HOME` 指定 SDK，或在 `client-android/local.properties` 写入
  `sdk.dir=...`

```bash
cd client-android
./gradlew :app:assembleDebug
./gradlew :app:testDebugUnitTest
./gradlew :app:lintDebug
```

Windows 使用 `gradlew.bat`。Debug APK 位于
`app/build/outputs/apk/debug/app-debug.apk`。

## Release 签名与 GitHub Actions

仓库中的 `.github/workflows/android.yml` 会在 PR 和 `main`/`master` push 上运行
Debug 构建、单元测试与 Lint。推送符合 `vMAJOR.MINOR.PATCH` 格式的 tag（例如 `v0.2.0`）
时，Workflow 会使用 GitHub Secrets 签名 Release APK、验证 APK 签名，并将 APK 附加到对应
GitHub Release。版本名取自 tag，Android `versionCode` 使用该 workflow 的运行序号。

### 配置 GitHub Secrets

在 GitHub 仓库页面进入 **Settings → Secrets and variables → Actions → New repository secret**，
添加以下仓库 Secrets：

| Secret | 内容 |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | `molisync-release.p12` 文件的单行 Base64 编码 |
| `ANDROID_KEYSTORE_PASSWORD` | 创建 PKCS#12 时使用的 keystore 密码 |
| `ANDROID_KEY_ALIAS` | `molisync` |
| `ANDROID_KEY_PASSWORD` | 私钥密码；你给出的 `keytool` 命令未单独设置 `-keypass`，通常与 keystore 密码相同 |

Windows PowerShell 可在本机生成 Base64 字符串（不要把输出贴到聊天、issue 或提交到仓库）：

```powershell
[Convert]::ToBase64String([IO.File]::ReadAllBytes(".\molisync-release.p12"))
```

把输出直接粘贴到 `ANDROID_KEYSTORE_BASE64` Secret。keystore 文件和密码都不要提交；项目
`.gitignore` 已忽略 `.p12` / `.pfx` 密钥库。Workflow 只在 `v*` tag 的 Release job 中读取这些
Secrets，不会在 PR 验证 job 中暴露它们。

### 在哪里签名

- **自动签名（推荐）**：配置以上 4 个 GitHub Secrets 后，推送 `v0.2.0` 这样的 tag。完成后在
  仓库的 **Releases** 页面下载签名 APK。APK 的签名证书由你生成的
  `molisync-release.p12` 提供。
- **Android Studio 手动签名**：选择 **Build → Generate Signed Bundle / APK… → APK**，选择
  `molisync-release.p12`、store type `PKCS12`、alias `molisync`，输入 keystore/key 密码，
  然后生成 Release APK。
- **本机 Gradle 签名**：设置 `ANDROID_KEYSTORE_PATH`、`ANDROID_KEYSTORE_PASSWORD`、
  `ANDROID_KEY_ALIAS` 和 `ANDROID_KEY_PASSWORD` 环境变量后运行
  `./gradlew :app:assembleRelease`（Windows 使用 `gradlew.bat`）。未配置签名变量时，
  本机 Release 仍按默认行为生成 unsigned APK；只设置了一部分签名变量会直接报错。

请将这份长期使用的发布 keystore 安全离线备份。丢失或更换应用签名密钥会影响用户升级；
签名 keystore 与应用运行时从 Android KeyChain 选择的 mTLS 客户端证书是两种不同用途的密钥。

## 首次配置

1. 打开应用，输入服务器基地址，例如 `https://sync.example.com`。
   只接受 HTTPS；地址中的用户名、查询串和片段会被拒绝。
2. 点击“选择客户端证书”，系统弹出 KeyChain 证书选择器（`KeyChain.choosePrivateKeyAlias`）。
3. 应用通过 `KeyChain.getCertificateChain` / `getPrivateKey` 读取证书链与私钥句柄，
   计算叶子证书的 SHA-256 指纹作为 `DeviceID`，并持久化证书别名与指纹。
4. 点击“保存并连接”，控制面 WebSocket 地址为 `<基地址>/api/v1/ws`。

私钥不会以任何形式导出到文件或 SharedPreferences；所有 TLS 签名都在系统 keystore 内完成。

首次配置保存后直接进入三栏主界面；后续启动若已保存服务器地址、证书别名与指纹则跳过配置页。
需要更换服务器或证书时，从右上角齿轮进入“设置 → 重新授权证书 / 断开并清除配置”。

## 三栏主界面与云同步

主界面分为 **文字 / 图片 / 文件** 三栏，数据来自服务端持久化记录，通过
`GET /api/v1/records` 拉取；控制面 WebSocket 的 `sync_record` 仅作为刷新信号，触发列表重载。

| 内容 | 规则 |
| --- | --- |
| 文字 | 上限 64 KiB，**始终**上传服务器；所有设备可见，可复制到剪贴板 |
| 图片 / 文件 | 单个 ≤ 设置阈值时，每次发送询问“保存到服务器”还是“P2P 直连”；> 阈值只允许 P2P |
| 阈值 | 默认 15 MiB，可在设置页按 1 MiB 步进调整到 5–50 MiB |
| 保存期限 | 服务端不自动过期；删除只能由发送者执行 |
| 下载 | `GET /api/v1/blobs/{id}`，`OkHttp` 断点续传（`.part` + `Range`）；完成后经 MediaStore 归入 `Pictures/MoliSync` / `Download/MoliSync`，Android 9 及以下回落公共目录 + MediaScanner |

顶栏“在线”胶囊显示在线设备数（其他在线设备 + 本机）；本机在 `DeviceDirectory.self`
中单独维护，因为服务端 `welcome` 的 `devices` 不包含自己。

上传实现要点：

- 上传前流式读取一遍 `content://` 计算整文件 BLAKE3，作为 `X-Blake3` 发送；
- 上传体是自定义 `RequestBody`，直接边读边写，不复制到临时文件；
- 失败时清理 `.part` 与已写入的半成品记录。

图片栏目前使用渐变占位卡片（显示名称、大小、下载状态），服务端不生成缩略图；
长按条目可“打开”或“在文件管理器打开”，下载完成的文件已归档到 `Pictures/MoliSync`。

文字预览超过 5 行时显示“点按展开全文”，详情弹窗内用 `SelectionContainer` 支持选取任意片段复制。

### 服务端证书信任

- 若存在 `app/src/main/assets/molisync_ca.crt`，控制面连接**只**信任该 CA 文件中的证书。
  适合自建私有 CA 的部署。修改后需要重新构建 APK。
- 若不存在该文件，使用 Android 系统信任库（适用于公网 CA）。
- 代码中没有 trust-all 分支，也不会在证书校验失败时降级。

对于 P2P 直连，服务端下发的对端 `DeviceID` 是目标设备证书的 SHA-256 指纹。客户端使用
`PeerFingerprintTrustManager` 做证书钉扎，并在握手后再次比对叶子证书指纹；由于对端证书
是身份证书而非域名证书，P2P 路径关闭了主机名校验，安全性由指纹钉扎保证。

## 分享与零拷贝传输

系统图库/文件管理器分享后：

1. `ShareParser` 只接受 `content://` URI；通过 `OpenableColumns` 读取文件名与大小，
   不读取文件内容。无法获得大小的条目会保留在列表中，但禁止发起传输。
2. 分享页先选择发送方式：
   - **保存到服务器**：仅当所有条目都在阈值内；逐文件流式上传，显示总进度；
   - **P2P 直连**：选择在线设备后由前台服务传输。
3. 选择 P2P 时，`ShareActivity` 才打开 `ParcelFileDescriptor`（模式 `r`），并随
   `TransferRequest` 一起交给前台服务。URI 的临时授权仍有效，服务在同一进程内直接持有句柄。
4. `TransferEngine` 每个文件使用一个复用的 1 MiB 缓冲区，边读边发送：
   - 分块 BLAKE3-256 与整文件 BLAKE3-256；
   - 49 字节帧头：`type(1) + fileIndex(4) + offset(8) + length(4) + hash(32)`；
   - 写帧后立即 flush，通知栏进度由 `ProgressThrottler` 限制到约 400 ms 一次。
5. 文件内容从不整体进入内存，也不写入应用缓存目录。

只接受 `content://`，可避免 `file://` 路径穿越与任意本地文件读取；文件名会去除控制字符、
路径分隔符，并限制为 255 字符。

## P2P 竞速

`PeerConnector` 接收控制面下发的 LAN/VPN 候选地址：

- LAN 候选立即并发探测，候选之间错开 50 ms；
- VPN 候选从 300 ms 开始探测；
- 总预算 1200 ms，超时后关闭所有候选 Socket；
- 获胜的 TCP Socket 升级为 mTLS，客户端证书来自 KeyChain；
- 对端证书指纹必须等于控制面给出的目标 `DeviceID`。

服务端通过 WebSocket 把一次性 32 字节会话票据同时推给双方。`P2pListener` 侧核对票据、
对端指纹与 `TransferRequest.selfDeviceId` 后接收数据并写入 MediaStore；`TransferEngine`
在写入 4 字节长度 + JSON manifest 时携带票据。任一候选都不可达时，发送端改用
`RelayTransport` 以 `X-Session-Ticket` 打开服务端中继，字节流走 `io.Copy` 转发。

取消传输时（通知栏“取消”或 Android 15 前台服务超时），会主动关闭 Socket，避免阻塞的
`OutputStream.write` 让协程无法退出。

## 前台服务与 Android 14/15

- Manifest 中声明 `android:foregroundServiceType="dataSync"` 和
  `FOREGROUND_SERVICE_DATA_SYNC` 权限。
- `ServiceCompat.startForeground(..., FOREGROUND_SERVICE_TYPE_DATA_SYNC)`，Android 8/9
  使用兼容分支。
- 通知渠道为低重要性、不响铃；通知包含进度、实时速率和“取消”动作。Android 13+ 的通知
  权限在设置页请求；即使用户拒绝，前台服务仍可运行。
- 传输期间持有 `PARTIAL_WAKE_LOCK`，结束时释放。
- Android 15 的 `dataSync` 超时通过 `onTimeout(startId, fgsType)` 处理：取消传输、
  发布失败通知并停止服务，避免被系统强制终止。
- 服务对 `ParcelFileDescriptor` 拥有所有权：正常结束、失败、取消、服务销毁都会兜底关闭。

## 模块结构

```
app/src/main/java/com/molirain/molisync/
├── MainActivity.kt                 根导航：配置 / 主界面 / 设置
├── MoliSyncApp.kt                  应用级依赖容器（控制面 + 云端仓库）
├── data/
│   ├── ClientSettings.kt           URL、别名/指纹、云端阈值持久化
│   ├── ControlPlaneClient.kt       mTLS WebSocket、hello/心跳/重连/设备表/同步信号
│   ├── ControlPlaneModels.kt       控制面 DTO 与端点校验
│   ├── DeviceDirectory.kt          在线设备与本机 StateFlow
│   └── cloud/
│       ├── CloudModels.kt          云端记录 DTO
│       ├── CloudRepository.kt      records/blob HTTP API、流式上传、Range 下载
│       └── FileDigest.kt           流式 BLAKE3
├── security/
│   ├── Crypto.kt                   SHA-256 / 指纹校验 / 常量时间比较
│   ├── KeyChainAdapter.kt          KeyChain 授权、身份加载、SSLContext
│   ├── MtlsClientFactory.kt        OkHttp mTLS 客户端（WS + HTTP）
│   └── ServerTrust.kt              CA 钉扎 / 系统信任 / P2P 指纹钉扎
├── share/
│   ├── ShareParser.kt              ACTION_SEND(_MULTIPLE)、content:// 元数据
│   └── ShareActivity.kt            分享 UI、云端/P2P 模式、PFD 打开、启动服务
├── transfer/
│   ├── ChunkedFileSender.kt        1 MiB 流式读取 + BLAKE3
│   ├── DataPlaneFrameWriter.kt     49 字节帧头
│   ├── PeerConnector.kt            LAN/VPN 竞速 + P2P mTLS
│   ├── TransferEngine.kt           多文件发送编排
│   ├── TransferModels.kt           请求校验/JSON/进度模型
│   ├── TransferNotifications.kt    dataSync 通知
│   ├── ProgressThrottler.kt        通知限频与速率计算
│   └── TransferService.kt          前台服务生命周期
└── ui/
    ├── HomeScreen.kt               三栏主界面、发送方式选择、进度
    ├── CloudViewModel.kt           记录列表、上传/下载、消息事件
    ├── SettingsScreen.kt           阈值、连接信息、重新授权、断开
    ├── SetupScreen.kt              首次配置
    ├── Formatters.kt               时间/大小/扩展名
    └── theme/Theme.kt              暖纸张 + 松绿配色
```

## 单元测试

`app/src/test` 覆盖可在 JVM 上验证的纯逻辑：

- BLAKE3 空输入与 `"abc"` 标准向量、分块哈希、整文件哈希；
- 49 字节帧头的大端字段、部分 payload、ABORT 帧截断；
- P2P manifest 往返、长度前缀校验、分片读取上限；
- `TransferRequest` JSON 往返、指纹/端点/URI/大小上限校验、仅中继模式；
- 控制面 snake_case DTO 映射、设备端点过滤与优先级；
- 云端记录 DTO 解析（扁平记录、可选字段、错误信封）；
- 文件名净化与扩展名格式化；
- SHA-256 / 指纹格式 / 常量时间比较。

```bash
./gradlew :app:testDebugUnitTest
```

共 44 个测试。Android 特有的 KeyChain、PFD、通知、前台服务与真实 mTLS 上传需要在
真机/模拟器上做集成验证，JVM 单元测试不能替代这些系统交互。

## 安全约束

- 私钥不导出、不落盘；SharedPreferences 只保存别名、证书指纹和服务器 URL。
- `allowBackup="false"`，并通过 `data_extraction_rules.xml` 排除 SharedPreferences 的
  云备份/设备迁移。
- `usesCleartextTraffic="false"`；服务器 URL 只接受 `https://`。
- OkHttp 禁用重定向与 SSL 重定向，避免客户端证书被转发到其他主机。
- 控制面使用系统/私有 CA 校验并保留主机名校验；P2P 使用目标证书指纹钉扎。
- 只接受 `content://` 分享 URI；不使用 `file://`、不复制到缓存、不读取未授权路径。
- 单次传输限制 100 个文件、总大小 1 TiB，防止大小求和溢出与异常元数据。

## 当前限制

- 图片栏只有渐变占位卡片，没有真实缩略图（Windows 客户端已支持缩略图缓存）。
- 服务端记录永不自动过期，也没有配额管理；长期使用会持续占用磁盘。
- P2P 依赖至少一条互达的 LAN/VPN 路径；跨 NAT 未做端口映射时只能走中继，吞吐受服务器带宽限制。
- 分享页的云端上传运行在 Activity 生命周期内（≤15 MiB，通常几秒）；超大文件只能走 P2P，
  由前台服务接管。
- 自动检查更新暂缓（仓库尚未公开），升级需手动安装新 APK。

# MoliSync Android 客户端

Kotlin + Jetpack Compose 客户端，面向 Android 8.0（API 26）及以上，`targetSdk 35`。
本模块聚焦三件事：

1. **系统 KeyChain mTLS**：证书与私钥由 Android 系统托管，应用只保存证书别名。
2. **系统分享零拷贝发送**：接管 `ACTION_SEND` / `ACTION_SEND_MULTIPLE`，直接流式读取
   `content://` 句柄，不把文件复制到应用缓存。
3. **前台数据同步服务**：大文件传输由 `dataSync` 类型的前台服务接管，通知栏实时展示
   速率/进度，并适配 Android 14 权限与 Android 15 `onTimeout`。

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

## 首次配置

1. 打开应用，输入服务器基地址，例如 `https://sync.example.com`。
   只接受 HTTPS；地址中的用户名、查询串和片段会被拒绝。
2. 点击“选择客户端证书”，系统弹出 KeyChain 证书选择器（`KeyChain.choosePrivateKeyAlias`）。
3. 应用通过 `KeyChain.getCertificateChain` / `getPrivateKey` 读取证书链与私钥句柄，
   计算叶子证书的 SHA-256 指纹作为 `DeviceID`，并持久化证书别名与指纹。
4. 点击“保存并连接”，控制面 WebSocket 地址为 `<基地址>/api/v1/ws`。

私钥不会以任何形式导出到文件或 SharedPreferences；所有 TLS 签名都在系统 keystore 内完成。

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
2. 用户选择接收设备并点击“开始传输”时，`ShareActivity` 才打开
   `ParcelFileDescriptor`（模式 `r`），并随 `TransferRequest` 一起交给前台服务。
   URI 的临时授权仍有效，服务在同一进程内直接持有句柄。
3. `TransferEngine` 每个文件使用一个复用的 1 MiB 缓冲区，边读边发送：
   - 分块 BLAKE3-256 与整文件 BLAKE3-256；
   - 49 字节帧头：`type(1) + fileIndex(4) + offset(8) + length(4) + hash(32)`；
   - 写帧后立即 flush，通知栏进度由 `ProgressThrottler` 限制到约 400 ms 一次。
4. 文件内容从不整体进入内存，也不写入应用缓存目录。

只接受 `content://`，可避免 `file://` 路径穿越与任意本地文件读取；文件名会去除控制字符、
路径分隔符，并限制为 255 字符。

## P2P 竞速

`PeerConnector` 接收控制面下发的 LAN/VPN 候选地址：

- LAN 候选立即并发探测，候选之间错开 50 ms；
- VPN 候选从 300 ms 开始探测；
- 总预算 1200 ms，超时后关闭所有候选 Socket 并抛出 `TransferException`；
- 获胜的 TCP Socket 升级为 mTLS，客户端证书来自 KeyChain；
- 对端证书指纹必须等于控制面给出的目标 `DeviceID`。

取消传输时（通知栏“取消”或 Android 15 前台服务超时），会主动关闭 Socket，避免阻塞的
`OutputStream.write` 让协程无法退出。

> 当前仓库阶段：服务端已实现控制面设备发现，但 P2P 信令、会话票据与中继管道属于后续
> 服务端里程碑；Android 端的数据面发送器已就绪，待对端监听/信令接入后即可端到端直传。
> 竞速超时会明确失败，不会静默降级为不安全的明文连接。

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
├── MainActivity.kt                 服务器 URL / KeyChain 授权 / 在线设备
├── MoliSyncApp.kt                  应用级依赖容器
├── data/
│   ├── ClientSettings.kt           URL 规范化、别名/指纹持久化
│   ├── ControlPlaneClient.kt       mTLS WebSocket、hello/心跳/重连/设备表
│   ├── ControlPlaneModels.kt       控制面 DTO 与端点校验
│   └── DeviceDirectory.kt          在线设备 StateFlow
├── security/
│   ├── Crypto.kt                   SHA-256 / 指纹校验 / 常量时间比较
│   ├── KeyChainAdapter.kt          KeyChain 授权、身份加载、SSLContext
│   ├── MtlsClientFactory.kt        OkHttp mTLS 客户端
│   └── ServerTrust.kt              CA 钉扎 / 系统信任 / P2P 指纹钉扎
├── share/
│   ├── ShareParser.kt              ACTION_SEND(_MULTIPLE)、content:// 元数据
│   └── ShareActivity.kt            分享 UI、PFD 打开、启动服务
├── transfer/
│   ├── ChunkedFileSender.kt        1 MiB 流式读取 + BLAKE3
│   ├── DataPlaneFrameWriter.kt     49 字节帧头
│   ├── PeerConnector.kt            LAN/VPN 竞速 + P2P mTLS
│   ├── TransferEngine.kt           多文件发送编排
│   ├── TransferModels.kt           请求校验/JSON/进度模型
│   ├── TransferNotifications.kt    dataSync 通知
│   ├── ProgressThrottler.kt        通知限频与速率计算
│   └── TransferService.kt          前台服务生命周期
└── ui/theme/Theme.kt               Compose 主题
```

## 单元测试

`app/src/test` 覆盖可在 JVM 上验证的纯逻辑：

- BLAKE3 空输入与 `"abc"` 标准向量、分块哈希、整文件哈希；
- 49 字节帧头的大端字段、部分 payload、ABORT 帧截断；
- `TransferRequest` JSON 往返、指纹/端点/URI/大小上限校验；
- 控制面 snake_case DTO 映射、设备端点过滤与优先级；
- SHA-256 / 指纹格式 / 常量时间比较。

```bash
./gradlew :app:testDebugUnitTest
```

共 31 个测试。Android 特有的 KeyChain、PFD、通知与前台服务需要在真机/模拟器上做集成验证，
JVM 单元测试不能替代这些系统交互。

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

- 本模块只实现“发送”方向；接收端的 HTTP Range 断点续传下载尚未包含在 Android 客户端中。
- 纯文本分享只展示文本，不写入系统剪贴板；剪贴板同步属于后续双轨同步接入。
- 服务端 P2P 信令、临时会话票据与中继转发尚未实现，因此当前无法完成端到端 P2P 传输。

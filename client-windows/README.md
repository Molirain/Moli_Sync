# MoliSync Windows Client

Windows 桌面客户端，Avalonia + C# / .NET 8 NativeAOT。目标是低常驻内存、流式 IO，并且**从始至终不导出客户端私钥**。

- 界面与 Android 客户端一致：文字、图片、文件、设置四个页面
- 首次启动只需要填写服务器地址；客户端证书自动从 Windows 个人证书库读取
- 默认使用 Windows 信任的根证书校验服务端；私有 CA 可一次性导入系统信任库
- WebSocket 长连接：TEXT 自动写剪贴板，FILE/IMAGE 仅落元数据占位
- 用户点击占位条目后才发起 HTTP Range 断点续传下载
- 数据目录可在安装时选择，也可在设置中带进度迁移；卸载不会删除数据目录
- 内置开机自启与关闭到托盘开关
- Happy Eyeballs 阶梯竞速：LAN 优先，300ms 后并发 VPN，1.2s 未连通则触发中继降级回调

## 目录

```text
client-windows/
├── src/MoliSync.Client/
│   ├── Security/       # CryptoAPI 证书枚举、筛选、服务端证书校验
│   ├── Net/            # Happy Eyeballs 竞速与连接结果
│   ├── Http/           # mTLS HttpClient、Range 断点续传
│   ├── Sync/           # WS 控制面、协议模型、占位存储、下载协调
│   ├── Platform/       # Win32 剪贴板（STA 线程）
│   ├── Services/       # 设置、云记录、设备目录、数据目录迁移、开机自启
│   ├── Views/          # Avalonia 页面与对话框
│   └── Program.cs      # NativeAOT GUI 入口
├── tests/MoliSync.Client.Tests/
└── MoliSync.Client.sln
```

## 构建与运行

要求：Windows 10/11、.NET 8 SDK 或更高版本；AOT 发布额外需要 VS 2022 的 C++ 生成工具（`link.exe`）。

```powershell
cd client-windows
dotnet build MoliSync.Client.sln -c Release
dotnet test  MoliSync.Client.sln -c Release

dotnet publish src/MoliSync.Client/MoliSync.Client.csproj -c Release -r win-x64 -o publish
.\publish\MoliSync.Client.exe
```

首次启动只需填写 HTTPS 服务器地址；客户端会列出 Windows 证书库中可用的客户端证书并自动选择。之后配置保存在：

- 设置：`%LOCALAPPDATA%\MoliSync\settings.json`
- 日志：`%LOCALAPPDATA%\MoliSync\logs\client.log`
- 数据：默认 `%USERPROFILE%\MoliSyncData`，可在设置中更改

`client.json` 只用于从旧版便携客户端导入配置，GUI 不再要求手工编辑。

## 便携性与安装

- NativeAOT 自包含发布，不需要预装 .NET 运行时。
- 安装包会把程序安装到当前用户目录，并把数据目录选择写入 `HKCU\Software\MoliSync\DataDirectory`；卸载时只删除程序，不删除数据目录。
- 不使用安装包时可以直接运行 `publish\MoliSync.Client.exe`，设置和数据目录仍写入上述用户目录。
- 仅在用户打开“开机自动启动”时写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`；关闭开关会删除该值。

## 配置

GUI 使用 `%LOCALAPPDATA%\MoliSync\settings.json`，字段如下：

| 字段 | 说明 |
| --- | --- |
| `serverUrl` | 服务端基地址，必须为 `https://`；可含反代子路径 |
| `caCertificatePath` | **可选**。仅用于显式钉扎私有服务端 CA；普通部署留空即可 |
| `expectedIssuerName` | 可选，与 `caCertificatePath` 配合做配置期一致性校验 |
| `clientCertificateThumbprint` | 自动写入上次使用的客户端证书指纹；证书被移除时会回退到最新可用证书 |
| `deviceName` | 设备显示名，1–64 个可打印字符 |
| `dataDirectory` | 下载文件与断点续传缓存目录，卸载不会删除 |
| `cloudThresholdBytes` | 云端阈值，默认 15 MiB，可在设置中调整 |
| `autoCopyText` | 收到 TEXT 是否自动写入剪贴板，默认 `true` |
| `autoStart` | 开机自启，默认关闭 |
| `startMinimized` | 开机自启时最小化到托盘，默认关闭 |
| `closeToTray` | 关闭窗口时最小化到托盘，默认开启 |
| `connectTimeoutSeconds` | 连接超时，默认 20 |

## 证书与 mTLS

### 枚举：直接使用 Windows 证书库

[WindowsCertificateStore.cs](src/MoliSync.Client/Security/WindowsCertificateStore.cs) 通过 `X509Store(StoreName.My, StoreLocation.CurrentUser)` 打开个人证书库。该 API 在 Windows 上就是 CryptoAPI `CertOpenSystemStore` / `CertEnumCertificatesInStore` 的托管封装，返回的证书持有系统证书上下文，私钥始终由 CNG 持有。

默认自动选择规则：

1. 必须 `HasPrivateKey`，且 RSA/ECDSA 私钥句柄可以实际获取（只获取句柄，不读取密钥材料）
2. 当前时间在 `NotBefore` / `NotAfter` 之内
3. `basicConstraints CA=false`（禁止把 CA 证书当客户端身份）
4. 优先选择显式带 `clientAuth (1.3.6.1.5.5.7.3.2)` 的证书；只有没有显式 clientAuth 证书时才回退到无 EKU 证书
5. 若保存了 `clientCertificateThumbprint`，优先使用该证书
6. 仍有多张时选择 `NotAfter` 最晚的一张

因此用户只需要把客户端证书导入“当前用户 → 个人 → 证书”，不需要在应用里选择文件，也不需要导出 PFX。

### 可选：钉扎私有 CA

如果部署要求只信任某个私有服务端 CA，可在 `settings.json` 中设置 `caCertificatePath`：

1. `IssuerName.RawData` 与钉扎 CA 的 `SubjectName.RawData` **逐字节相等**（不做 CN 模糊匹配，防止同名伪造 CA）
2. 使用 `X509ChainTrustMode.CustomRootTrust` 仅以钉扎 CA 为信任锚重建证书链（禁止系统根库扩大信任）
3. 服务端证书同样只按该 CA 校验
4. 客户端证书链（含中间 CA）必须已安装，否则 `Chain` 构建会失败

这是可选的加固项；默认路径不要求 CA 文件。

### 连接：系统安全句柄直连

[ControlPlaneClient.cs](src/MoliSync.Client/Sync/ControlPlaneClient.cs) 与 [MtlsHttpClientFactory.cs](src/MoliSync.Client/Http/MtlsHttpClientFactory.cs) 把 `ClientIdentity.Certificate` 直接交给 `ClientWebSocket` / `SocketsHttpHandler`。TLS 握手由 SslStream 通过 CNG 句柄完成签名，代码中**没有任何私钥导出路径**（无 PFX/PEM 导出调用）。

服务端证书由 [PinnedServerCertificateValidator.cs](src/MoliSync.Client/Security/PinnedServerCertificateValidator.cs) 校验：

- 未配置 `caCertificatePath` 时使用 Windows 信任库；私有 CA 需要一次性导入“受信任的根证书颁发机构”
- 配置了 `caCertificatePath` 时改用该 CA 重建链
- 拒绝 `SslPolicyErrors.RemoteCertificateNameMismatch`（主机名校验不放松）
- HTTP 客户端关闭自动重定向，避免把客户端证书发送到意外源站
- CRL 吊销由反向代理在握手层强制执行，客户端不依赖本地 CRL 文件

## 控制面协议

连接地址：`wss(s)://<serverUrl>/api/v1/ws`，证书在 TLS 握手中提交。所有帧为单个 JSON 文本对象，字段命名与服务端 [proto/sync.proto](../proto/sync.proto) 的 snake_case 映射一致。

- 连接后发送 `hello`（`device.name` / `device.platform` / `endpoints`）
- 收到 `welcome` 后按服务端下发的 `heartbeat_interval_ms` 发送 `heartbeat`，并将值限制在 5s–5min，防止异常值造成忙循环
- 断线后指数退避（1s → 30s，±20% 抖动）重连
- `sync_record.record` 为嵌套对象，避免记录的 `type` 与服务端信封的 `type` 冲突

[SyncDispatcher.cs](src/MoliSync.Client/Sync/SyncDispatcher.cs) 的分发规则：

| 记录类型 | 行为 |
| --- | --- |
| `text` | 全量写入 [SyncItemStore](src/MoliSync.Client/Sync/SyncItemStore.cs)，并通过 Win32 写入剪贴板 |
| `image` / `file` | 仅保存 `file_meta` 占位，绝不自动下载文件本体 |
| 其他 | 记录日志并忽略，保证向前兼容 |

占位条目通过 `SyncItemStore.ItemAdded` / `ItemUpdated` 暴露给 UI；真实托盘界面可直接订阅这两个事件。

## 剪贴板

[WindowsClipboardSink.cs](src/MoliSync.Client/Platform/WindowsClipboardSink.cs) 在独立 STA 线程上执行：

1. `OpenClipboard` 重试（最多 20 次 × 50ms），应对其他进程占用
2. `EmptyClipboard` → `GlobalAlloc(GMEM_MOVEABLE)` → `GlobalLock`
3. 写入 UTF-16 文本与终止符，`SetClipboardData(CF_UNICODETEXT)` 成功后内存所有权交给系统
4. 任一步失败都会 `GlobalFree` 并 `CloseClipboard`

采用“最新值覆盖”语义：连续同步多条文本时只写最后一条，避免积压陈旧内容。

## 断点续传下载

[BlobDownloader.cs](src/MoliSync.Client/Http/BlobDownloader.cs) 流程：

1. 文件名先做净化：移除目录段、非法字符、Windows 保留设备名，限制长度，并再次校验解析后的绝对路径必须位于下载目录内
2. 未完成内容写入 `<blobId>.part`；存在时以 `Range: bytes=<length>-` 续传
3. `206`：严格校验 `Content-Range` 的起点与总长度；`200`：说明服务端忽略 Range，截断 `.part` 从零重写，绝不重复拼接
4. 服务端若对单次 Range 响应做了分片限制，客户端会继续请求剩余区间
5. `416` 且本地长度已等于声明大小时直接完成
6. 完成后校验总长度，并以 `File.Move(..., overwrite:false)` 原子落位；重名时追加 ` (1)`、` (2)` 后缀
7. 网络类错误最多重试 3 次，保留 `.part` 以便继续断点续传

用户点击流程：UI 调用 [SyncCoordinator.DownloadAsync](src/MoliSync.Client/Sync/SyncCoordinator.cs)；状态依次变为 `Downloading → Downloaded / Failed`，取消时回到 `Placeholder`。

## Happy Eyeballs 竞速

[HappyEyeballsConnector.cs](src/MoliSync.Client/Net/HappyEyeballsConnector.cs) 的默认时序：

```text
T=0ms       LAN 候选按 priority 降序、每 50ms 错峰发起
T=300ms     VPN 候选开始并发探测（LAN 仍在继续）
T=1200ms    全部未成功 → 触发中继降级
```

- 任一 TCP 连接成功即取消其余探测；所有失败探测的 socket 都会被释放
- 去重 `(ip, port)`，过滤非法 IP / 端口 / IPv6 link-local
- 所有候选快速失败时立即降级，而不是空等到 1.2s；只有仍有可能成功的探测才等到截止时间
- 结果是 `PeerConnection`：`LAN` / `VPN` / `Relay` 三种路径，UI 可据此显示“局域网直连 / VPN 专线 / 服务器中继”

降级回调契约：

```csharp
new HappyEyeballsOptions
{
    TotalTimeout = TimeSpan.FromMilliseconds(1200),
    VpnStaggerDelay = TimeSpan.FromMilliseconds(300),
    RelayFallback = async (context, cancellationToken) =>
    {
        // context.Reason: Timeout | AllCandidatesFailed
        // context.Failures: 每个候选的失败原因
        // 在此打开服务端中继传输，并完成端到端会话票据校验
        return PeerConnection.FromRelay(relayStream);
    },
}
```

未配置回调时抛出 `RelayFallbackRequiredException`，异常携带完整的 `RelayFallbackContext`。竞速模块只负责“选择最优路径”，**在拿到连接后必须先完成对端身份认证（mTLS / 会话票据证明）再发送业务数据**。

## 控制台 UI

```
list                 查看同步历史
download <recordId>  下载文件/图片占位（支持断点续传）
help / quit
```

占位条目与远端文本在输出前会过滤控制字符，避免终端转义序列注入。真实 UI 可复用 `SyncItemStore`、`SyncDispatcher.PresenceChanged` 与 `SyncCoordinator`，无需改动网络层。

## 测试

```powershell
dotnet test MoliSync.Client.sln -c Release
```

52 项测试，覆盖：

- 证书筛选：正确签发、CA 证书拒绝、过期、EKU、无私钥、同名伪造 CA、链校验、指纹钉扎、最新证书选择
- 服务端证书钉扎：错误 CA 拒绝、正确 CA 接受、主机名不匹配拒绝
- Happy Eyeballs：LAN 直连、LAN 失败后 VPN 延迟启动、超时降级、全部失败降级、无回调异常、取消、候选去重
- Range 下载：续传、服务端忽略 Range、完整 `.part` 直接落位、零字节、路径穿越净化、大小不符、进度单调
- 事件分发：TEXT 剪贴板、重复记录、FILE/IMAGE 占位、缺失元数据、欢迎帧心跳钳制、在线事件、剪贴板失败不丢记录

已通过 `dotnet publish -c Release -r win-x64` 的 NativeAOT 实际编译验证。

## 当前边界

- 客户端尚未启动 P2P 监听器，因此 `hello.endpoints` 上报空列表（服务端接受空候选池）。P2P 监听、端点枚举与信令属于下一阶段，本次交付的是竞速与连接结果模块。
- 服务端中继端点属于 MoliSync 服务端 Milestone 3；竞速模块通过强类型回调/异常交付降级决策，不伪造中继隧道。
- 本地不解析 CRL，吊销由反向代理在 mTLS 握手层实时执行（服务端作为信任仲裁者）。

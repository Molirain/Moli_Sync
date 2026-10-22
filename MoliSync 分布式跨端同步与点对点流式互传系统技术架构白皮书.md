# 《MoliSync 分布式跨端同步与点对点流式互传系统技术架构白皮书》

* **项目代号**：MoliSync
* **基准域名**：`sync.molirain.com`
* **安全基线**：复用宿主机私有 PKI（`/home/camellia/npm/data/mtls/`），全链路 mTLS + 端到端加密（E2EE）
* **目标平台**：Debian 13（Server）、Windows 10/11（Client）、Android 10+（Client）

---

## 一、 系统架构总览

MoliSync 采用控制面中心化（Nginx mTLS 网关卸载 + Debian 核心服务）**与**数据面双轨制（P2P 竞速直连 + 透明流式中继）的混合架构。

```text
+-----------------------------------------------------------------------------------+
|                              客户端层 (Client Layer)                               |
|  [Windows 客户端]                                             [Android 客户端]      |
|  - 自动检索 Windows Cert Store (MY)                           - KeyChain 系统钥匙串 |
|  - 系统托盘 + 全局剪贴板钩子                                    - Direct Share 注入  |
+-------------------+-------------------------------------------+-------------------+
                    | 1. 控制面与云同步 (HTTPS / WSS)             |
                    v                                           v
+-----------------------------------------------------------------------------------+
|                        接入网关层 (Nginx / NPM Container)                          |
|  - 挂载 /data/mtls/ca.crt & crl.pem 执行 mTLS 强校验与 CRL 吊销拦截                 |
|  - 提取证书指纹 ($ssl_client_fingerprint) 与身份 ($ssl_client_s_dn) 注入 Header     |
+-------------------+-------------------------------------------+-------------------+
                    | 2. 内部反向代理 (HTTP/1.1 Upgrade & gRPC)   |
                    v                                           v
+-----------------------------------------------------------------------------------+
|                      核心服务层 (MoliSync Server on Debian)                        |
|  - [Device Registry] 维护设备在线状态与多网卡 IP 候选池 (LAN / VPN)                 |
|  - [Sync Engine]     SQLite 存储全量文本 & 文件元数据/WebP 缩略图，实时扇出广播      |
|  - [Stream Relay]    零落盘内存 RingBuffer 管道，提供 NAT 穿透失败时的加密字节流透传 |
+-----------------------------------------------------------------------------------+

```

---

## 二、 PKI 证书复用与端侧无感接入设计

系统完全复用现有 `/home/camellia/npm/data/mtls/` 下的 CA 体系与 CRL 吊销列表，无需重复建设证书链。

### 1. 网关层身份透传（Nginx 配置扩展）

客户端与 `sync.molirain.com` 通信时，由 Nginx 完成 mTLS 握手与 CRL 校验，并将客户端证书的关键信息透传给后端的 MoliSync 服务：

```nginx
location / {
    proxy_pass http://127.0.0.1:9520;
    proxy_http_version 1.1;
    
    # 支持 WebSocket 与流式长连接
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_request_buffering off; # 禁用请求缓冲，实现大文件中继零落盘流式直通
    proxy_buffering off;

    # 将 mTLS 校验结果与证书唯一标识透传给后端
    proxy_set_header X-Client-Verify $ssl_client_verify;
    proxy_set_header X-Client-DN $ssl_client_s_dn;
    proxy_set_header X-Client-Fingerprint $ssl_client_fingerprint;
    proxy_set_header X-Client-Cert $ssl_client_escaped_cert;
}

```

* **设备唯一身份（DeviceID）生成规则**：后端直接读取 `X-Client-Fingerprint`（证书 SHA-256 指纹）或解析 `X-Client-DN` 中的 `CN` 字段作为 `DeviceID`，天然防伪造。

### 2. Windows 端：零文件导入，自动检索系统证书库

由于电脑已安装个人客户端证书，MoliSync Windows 端无需手动指定 `.p12` 文件路径，直接调用 Windows CryptoAPI（`crypt32.dll`）读取系统个人证书存储区（`CurrentUser\MY`）：

1. **自动匹配逻辑**：遍历 `CurrentUser\MY` 下的所有证书，筛选出颁发者（Issuer）匹配 `/data/mtls/ca.crt` Subject（如 `CN=Molirain Root CA`）且包含客户端认证扩展（`Client Authentication, 1.3.6.1.5.5.7.3.2`）的有效证书。
2. **私钥免导出调用**：通过 Windows CNG（Cryptography API: Next Generation）接口获取 `NCryptKeyHandle`，直接在系统安全上下文中完成 TLS 握手签名，私钥全程不落盘、不暴露在应用内存中。

### 3. Android 端：类 Bitwarden 系统钥匙串（KeyChain）授权

Android 端首次启动进入引导页，交互流程与 Bitwarden 完全一致：

1. **填写服务器地址**：用户输入 `[https://sync.molirain.com](https://sync.molirain.com)`。
2. **调起系统证书选择器**：点击“选择客户端证书”，触发系统级 `KeyChain.choosePrivateKeyAlias()`：
```kotlin
KeyChain.choosePrivateKeyAlias(
    activity,
    { alias -> 
        // 用户点击允许后，仅持久化保存证书别名 (例如 "molirain-phone-a")
        prefs.edit().putString("mtls_cert_alias", alias).apply()
    },
    arrayOf("RSA", "EC"), // 支持的算法类型
    null,                 // 可选指定 Root CA Principal 自动过滤
    "sync.molirain.com", 443, null
)

```


3. **构建 SSLContext**：后续启动时，通过保存的 `alias` 调用 `KeyChain.getPrivateKey(context, alias)` 和 `KeyChain.getCertificateChain(context, alias)` 构建自定义 `X509ExtendedKeyManager`，直接驱动 OkHttp / WebSocket 与 P2P 传输套接字。

### 4. 设备间 P2P 直连时的证书校验（防内网中间人）

当设备 A 与设备 C 在局域网或 VPN 内不经过 Nginx 直接建立点对点连接时：

* **首次连接控制面时**：客户端自动从服务端拉取一份最新的 `ca.crt` 与 `crl.pem` 缓存在本地沙盒。
* **P2P 双向握手**：设备 C（临时 Server）与设备 A（临时 Client）均出示各自在系统证书库中的同一张客户端证书，双方使用本地缓存的 `ca.crt` 验证对方证书链及 `crl.pem` 吊销状态，并核对证书指纹是否与信令下发的目标 `DeviceID` 一致。

---

## 三、 核心通信协议与数据结构（Protobuf / JSON 规范）

### 1. 控制面信令协议（WebSocket `/api/v1/ws`）

```protobuf
syntax = "proto3";
package molisync;

// 设备端点信息
message Endpoint {
  enum NetType {
    LAN = 0;   // 局域网物理网卡 (如 192.168.x.x, 10.x.x.x)
    VPN = 1;   // 虚拟组网网卡 (如 Tailscale 100.64.x.x / EasyTier)
  }
  NetType type = 1;
  string ip = 2;
  uint32 port = 3;
  uint32 priority = 4; // LAN 默认 100, VPN 默认 50
}

// 1. 设备注册与心跳上报 (Client -> Server)
message DeviceHeartbeat {
  string device_id = 1;           // 证书指纹或 CN
  string device_name = 2;         // 自定义别名，如 "手机 A"、"电脑 C"
  string platform = 3;            // "windows" | "android"
  repeated Endpoint endpoints = 4;// 本机监听的 P2P 端口与多网卡 IP
}

// 2. 云端同步条目 (双向广播：三端云同步模式)
message SyncRecord {
  string record_id = 1;           // UUID v7 (自带时间序)
  string sender_device_id = 2;
  int64 created_at = 3;           // Unix 毫秒时间戳
  
  enum PayloadType {
    TEXT = 0;                     // 纯文本 (全量下发)
    IMAGE = 1;                    // 图片 (仅下发元数据 + WebP 缩略图)
    FILE = 2;                     // 小文件 (仅下发元数据)
  }
  PayloadType type = 4;

  // 当 type == TEXT 时填充，接收端直接落地并写入剪贴板
  string text_content = 5;

  // 当 type == IMAGE 或 FILE 时填充，接收端仅显示占位卡片
  CloudFileMeta file_meta = 6;
}

message CloudFileMeta {
  string blob_id = 1;             // 服务端存储索引 ID
  string file_name = 2;           // 原始文件名
  int64 file_size = 3;            // 字节大小
  string mime_type = 4;
  string blake3_hash = 5;         // 全文件 BLAKE3 校验值
  bytes thumbnail_webp = 6;       // 仅图片有效：<=15KB 的模糊预览图
}

// 3. 大文件互传握手信令 (Client A -> Server -> Client B)
message P2PTransferOffer {
  string session_id = 1;
  string source_device_id = 2;
  string target_device_id = 3;
  repeated Endpoint source_endpoints = 4; // 发送方或接收方的候选 IP 列表
  
  message FileEntry {
    uint32 file_index = 1;
    string relative_path = 2;
    int64 file_size = 3;
    string blake3_hash = 4;
  }
  repeated FileEntry files = 5;
}

```

### 2. P2P 与流式中继二进制帧格式（Data Plane）

一旦传输通道（LAN 直连、VPN 直连、或 Debian 中继流）建立并完成端到端 TLS 握手，通道内采用极简的二进制帧以最大化吞吐量：

```text
+----------------+----------------+----------------+------------------------+
| Frame Type(1B) | File Index(4B) |  Offset (8B)   |   Payload Length (4B)  |
+----------------+----------------+----------------+------------------------+
|                       BLAKE3 Chunk Hash (32 Bytes)                        |
+---------------------------------------------------------------------------+
|                     Chunk Raw Data (默认 4MB ~ 16MB)                       |
+---------------------------------------------------------------------------+

```

* **Frame Type**：`0x01`（数据包）、`0x02`（断点续传 Bitfield 请求）、`0x03`（文件校验完成 ACK）、`0xFF`（传输中止）。

---

## 四、 多 IP 寻址与动态竞速状态机（Happy Eyeballs）

每个设备在本地维护一个监听了动态端口（如 `9527`）的 mTLS TCP Server，并将本机过滤后的网卡 IP（剔除环回、Docker `172.17.x.x` 网段，保留物理局域网与 Tailscale/EasyTier 虚拟网卡 IP）实时同步给 Debian。

```text
[发起互传: 手机 A -> 电脑 C]
       |
       +---> 1. 从本地状态表获取 "电脑 C" 的 Endpoints:
       |        - LAN: 192.168.1.50:9527
       |        - VPN: 100.64.0.8:9527
       |
       +---> 2. 启动阶梯并发探测 (Happy Eyeballs):
                ├── [T = 0ms]   发起 TCP Dial -> 192.168.1.50:9527 (Timeout: 350ms)
                ├── [T = 150ms] 若 LAN 未连通，并发发起 TCP Dial -> 100.64.0.8:9527 (Timeout: 800ms)
                |
                +---> [分支 A: 任一 IP 握手成功]
                |        ├── 立即取消其余探测协程
                |        ├── 执行端到端 mTLS 握手 (校验电脑 C 的证书指纹)
                |        └── UI 状态栏显示: "⚡ 局域网直连中" 或 "🛡️ VPN 专线直连中"
                |
                +---> [分支 B: 950ms 内全部超时/不可达]
                         ├── 触发 Fallback，向 Debian 发起流式管道请求:
                         |   CONNECT /api/v1/relay?session_id=xxx
                         ├── UI 弹出显著提示 (Toast / 通知栏):
                         |   "⚠️ 内网与 VPN 均不可达，已切换至 Debian 服务器中继模式"
                         ├── 在中继字节流管道之上执行 A 与 C 的端到端 mTLS 二次握手
                         └── 开始流式分块传输 (Debian 仅做内存 RingBuffer 转发，零落盘)

```

---

## 五、 双模式业务流与存储设计

### 模式一：三端云同步模式（文字 / 图片 / 小文件）

1. **上传与入库**：
* 手机 A 或电脑 C 复制文本或分享小文件至“云同步空间”。
* 文本直接封装在 `SyncRecord` 中通过 WebSocket 发送；图片/小文件通过 `POST /api/v1/blobs` 流式上传至 Debian 的 `/var/lib/molisync/blobs/` 目录。


2. **元数据广播与按需下载（Metadata-Only Sync）**：
* Debian 将记录写入 SQLite，并向所有在线设备推送 `SyncRecord`。
* **若是 `TEXT**`：所有在线设备立即收到全文，写入本地历史列表并可配置自动写入系统剪贴板。
* **若是 `IMAGE` / `FILE**`：接收端**绝不自动下载文件本体**，仅将 `file_name`、`file_size`、`blake3_hash` 和 15KB 的 `thumbnail_webp` 存入本地数据库。
* **UI 呈现**：客户端列表显示文件名、大小、高清毛玻璃缩略图及一个“云端未下载”图标。只有当用户点击该文件或点击“另存为”时，才发起 `GET /api/v1/blobs/{blob_id}`（支持 `Range` 断点续传）将文件拉取到本地。



### 模式二：大文件互传模式（P2P / 流式中继）

1. **触发**：用户在文件管理器或图库长按多个大文件（如数 GB 的视频或固件包），选择 MoliSync 并点击目标设备“电脑 C”。
2. **连接建立**：按第四节的 Happy Eyeballs 竞速建立 P2P 连接或中继管道。
3. **零拷贝流式发送**：
* 发送端以 4MB 为单位从文件描述符读取数据，使用 CPU 多核计算 BLAKE3 块哈希并写入 TLS 套接字。
* 若走 Debian 中继，Debian 端为每个 Session 分配固定 `8MB` 的内存 RingBuffer（`io.CopyBuffer`），发送端写多少、接收端读多少，利用 TCP 滑动窗口背压（Backpressure）自然限速，**服务端磁盘写入量为 0**。



---

## 六、 数据库 Schema 与服务端目录规划（Debian）

### 1. 宿主机与容器挂载规划

```text
/home/camellia/npm/data/mtls/
├── ca.crt                  # 现有根证书 (只读挂载至 Nginx 与 MoliSync 容器)
└── crl.pem                 # 现有吊销列表 (只读挂载至 Nginx 与 MoliSync 容器)

/var/lib/molisync/
├── meta.db                 # SQLite 数据库 (WAL 模式，存储设备信息与同步元数据)
└── blobs/                  # 云同步模式下的小文件/图片实体存储 (支持定时清理策略)

```

### 2. SQLite 核心表结构

```sql
PRAGMA journal_mode = WAL;

-- 1. 已认证设备表
CREATE TABLE devices (
    device_id TEXT PRIMARY KEY,          -- 证书 SHA256 指纹
    common_name TEXT NOT NULL,           -- 证书 CN
    device_alias TEXT NOT NULL,          -- 显示名称 (如 "手机 A", "电脑 C")
    platform TEXT NOT NULL,              -- 'android' | 'windows'
    last_seen_at INTEGER NOT NULL
);

-- 2. 三端云同步记录表 (文字全量 + 文件元数据)
CREATE TABLE sync_records (
    record_id TEXT PRIMARY KEY,          -- UUID v7
    sender_id TEXT NOT NULL,
    payload_type INTEGER NOT NULL,       -- 0: TEXT, 1: IMAGE, 2: FILE
    text_content TEXT,                   -- 仅 TEXT 类型非空
    blob_id TEXT,                        -- 关联文件存储 ID
    file_name TEXT,                      -- 文件名 (客户端未下载时仅展示此字段)
    file_size INTEGER,
    mime_type TEXT,
    blake3_hash TEXT,
    thumbnail_webp BLOB,                 -- 微型预览图
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_sync_created ON sync_records(created_at DESC);

```

---

## 七、 客户端平台工程规范

| 模块特性 | Windows 客户端 | Android 客户端 |
| --- | --- | --- |
| **证书加载方式** | 调用 Win32 `CertOpenSystemStore("MY")` 自动匹配 `ca.crt` 签发的证书，免手动导入 | 首次启动输入 `sync.molirain.com` 并调起 `KeyChain.choosePrivateKeyAlias` 授权 |
| **系统级分享入口** | 注册系统右键菜单 `SendTo`（发送到 MoliSync）+ 托盘图标拖拽浮窗 | 注册 `ACTION_SEND` / `ACTION_SEND_MULTIPLE` + `ShortcutManagerCompat` Direct Share 直达设备 |
| **后台保活与传输** | 单实例后台进程 + 系统托盘驻留 | 传输时启动 `ForegroundService`（类型：`dataSync`），绑定通知栏实时进度条 |
| **本地文件占位呈现** | 主界面列表仅渲染文件名、大小与图标，双击或点击下载按钮触发流式落盘 | 列表渲染文件名与内存级 `thumbnail_webp`，点击后下载至 `Downloads/MoliSync/` 并触发系统打开 |

---

## 八、 分阶段研发路线图（Milestones）

1. **Milestone 1：服务端骨架与 Nginx mTLS 联调**
* 配置 `sync.molirain.com` 的 Nginx 反向代理，挂接 `/data/mtls/ca.crt` 与 `crl.pem`。
* 编写 Go 服务端，验证解析 `X-Client-Fingerprint` 头以及 `/api/v1/ca-bundle`（下发 `ca.crt` 供客户端 P2P 互验）接口。


2. **Milestone 2：三端云同步（文字与元数据占位）**
* 实现 SQLite 持久层、WebSocket 广播中心与 `/api/v1/blobs` 分块上传/Range 下载接口。
* 完成客户端“首次 URL + 证书绑定”引导页，跑通文字直接同步、文件仅同步文件名并支持点击按需下载的闭环。


3. **Milestone 3：多 IP 注册、Happy Eyeballs 竞速与透明中继**
* 客户端实现本地 LAN / VPN 网卡枚举上报与 P2P mTLS 监听服务。
* 实现并发拨号器、1.2s 超时降级中继管道与 UI 降级提示弹窗，接入 Android `Direct Share` 系统分享面板。
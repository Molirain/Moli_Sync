window.MoliSyncI18n.add("zh-CN", {
  meta: {
    title: "MoliSync — 自托管的跨端同步",
    description: "自托管的跨端传输与同步。mTLS 身份、300 ms 一档的 LAN/VPN 候选探测，以及 1.2 秒回退的零落盘中继。",
    ogDescription: "mTLS 身份、49 字节帧与零落盘中继。一个 Go 控制面，Windows 与 Android 客户端。"
  },

  a11y: {
    skip: "跳到主要内容",
    langSwitch: "语言"
  },

  nav: {
    features: "特性",
    deploy: "部署",
    docs: "文档",
    privacy: "隐私",
    github: "GitHub"
  },

  hero: {
    kicker: {
      selfHosted: "自托管",
      mtls: "mTLS 身份",
      p2p: "P2P 流式传输"
    },
    title: "跨端同步，<br><span class=\"text-accent\">只走你自己的网络。</span>",
    lead: "一个 Go 控制面，Windows 与 Android 客户端。数据面每 300 ms 探测一档 LAN/VPN 候选地址；1.2 秒内没有响应，就交给服务端零落盘中继。身份是客户端证书的 SHA-256 指纹，由你的反向代理校验。",
    cmd: {
      tabs: "安装方式",
      copy: "复制",
      copied: "已复制",
      note: "Docker 24+ · Compose 需在仓库根目录执行 · :9520 不对外发布"
    },
    facts: {
      arch: "linux/amd64 · arm64",
      runtime: "nonroot · 只读",
      releases: "发布版 ↗"
    },
    diagram: {
      heading: "数据路径",
      range: "300 ms 一档 · 1.2 秒预算",
      alt: "时序图：代理/控制面向两端签发一次性 32 字节票据。两端按 300 ms 一档并发探测 LAN/VPN 候选，在 1.2 秒预算内完成 mTLS，随后传输 49 字节帧；没有直连应答时，由 io.Copy 中继，全程零落盘。",
      unit: "ms",
      control: "代理 / 控制面",
      direct: "直连",
      mtls: "mTLS · SHA-256",
      ticket: "票据 · 32 B",
      race: "LAN / VPN 候选 · 300 ms",
      candidate: "候选地址 · 300 ms 一档",
      frames: "49 B",
      framesLong: "49 B 数据帧",
      relay: "中继",
      fallback: "1.2 秒",
      deadline: "1.2 秒截止",
      budget: "≤ 1.2 秒",
      noDirect: "无直连",
      relayCopy: "io.Copy 中继",
      zeroDisk: "零落盘",
      ticketNote: "一次性 32 B 票据",
      pairing: "15 秒配对超时",
      caption: "直连优先 · 1.2 秒回退中继 · 零落盘"
    },
    path: {
      tabs: "数据路径模式",
      tabDirect: "P2P 直连",
      tabRelay: "中继回退",
      statusDirect: "LAN/VPN · Happy Eyeballs",
      connected: "已连接",
      timeoutShort: "超时 > 1.2 秒",
      serverPipe: "服务端管道",
      ticket: "会话票据 · 32 B",
      idle: "休眠",
      happy: "Happy Eyeballs · 300 ms",
      directStream: "直连流 · WireGuard/LAN",
      zeroServer: "服务端零负载",
      frames: "49 B 帧",
      directNote: "端到端 TLS · 服务端零字节",
      timeout: "LAN/VPN 超时（> 1.2 秒）",
      encrypted: "加密流",
      proxyRelay: "代理 · 中继",
      relayBox: "io.Copy RingBuffer",
      relayDisk: "0 B 落盘",
      e2e: "经服务端管道的端到端加密",
      captionDirect: "直连 · 服务端零负载 · 端到端 TLS",
      captionRelay: "超时 > 1.2 秒 · io.Copy 中继 · 零落盘",
      altDirect: "直连时序：代理向两端签发会话票据，Windows 用 Happy Eyeballs 探测 Android 300 ms，随后两端通过绿色 TLS 直连管道传输，代理保持空闲。",
      altRelay: "中继时序：LAN/VPN 探测在 1.2 秒后超时。Windows 的加密流进入代理的 io.Copy RingBuffer，再转发到 Android；服务端零落盘。"
    }
  },

  features: {
    eyebrow: "能力 / 06",
    title: "协议实际做了什么。",
    lead: "没有仪表盘演示。下面每一条都对应线上格式、客户端行为或一个部署默认值。",
    items: [
      {
        tag: "身份",
        title: "身份来自证书，不来自共享密钥。",
        body: "控制面以反向代理校验过的客户端证书 SHA-256 指纹生成 DeviceID；入站身份头先清除，再写入代理的校验结果。",
        metric: "SHA-256 · 受信 CIDR"
      },
      {
        tag: "选路",
        title: "Happy Eyeballs，加上硬性截止时间。",
        body: "两端广告 LAN/VPN 候选地址，接收方按 300 ms 阶梯并发探测；1.2 秒内无人应答，会话就转给中继。",
        metric: "300 ms · 1.2 s"
      },
      {
        tag: "中继",
        title: "中继不落盘。",
        body: "服务端配对两端、校验一次性 32 字节票据，再用 io.Copy 转发字节；15 秒配对超时。",
        metric: "0 B 落盘 · 15 s"
      },
      {
        tag: "分帧",
        title: "49 字节帧，可续传读取。",
        body: "数据面使用 49 字节定长帧，Android 额外做 BLAKE3 校验；两端 blob 下载都支持 HTTP Range。",
        metric: "49 B · BLAKE3 · Range"
      },
      {
        tag: "记录",
        title: "默认持久，只有发送者能删。",
        body: "≤64 KiB 的文字存入 SQLite；图片与文件流式写入 blob 目录。记录不会自动过期，只有发送者能删除。",
        metric: "64 KiB · 仅发送者可删"
      },
      {
        tag: "运行",
        title: "Distroless、nonroot、多架构。",
        body: "服务端镜像支持 linux/amd64 与 linux/arm64，以 nonroot 运行，根文件系统只读，无 shell、无包管理器。",
        metric: "amd64 · arm64 · 只读"
      }
    ]
  },

  spec: {
    items: [
      { label: "文字上限", value: "64", unit: "KiB", note: "单条记录，存入 SQLite" },
      { label: "P2P 阈值", value: "15", unit: "MiB", note: "默认值，可在 5–50 MiB 调整" },
      { label: "中继截止", value: "1.2", unit: "s", note: "300 ms 一档候选探测" },
      { label: "数据帧", value: "49", unit: "B", note: "两端固定帧长" }
    ]
  },

  docs: {
    eyebrow: "文档",
    title: "把它部署在你的反向代理之后。",
    lead: "示例 Compose 不会发布 9520 端口。由代理终止 mTLS、清除入站 X-Client-* 头，再写入新的已校验值。",
    server: "服务端配置",
    windows: "Windows 客户端",
    android: "Android 客户端"
  },

  footer: {
    tagline: "自托管的跨端传输与同步。你的服务器、你的证书、你的网络路径。",
    project: "项目",
    docs: "文档",
    links: {
      source: "源码",
      releases: "发布版本",
      protocol: "协议定义",
      whitepaper: "架构白皮书",
      server: "服务端配置",
      windows: "Windows 客户端",
      android: "Android 客户端",
      issues: "问题反馈",
      privacy: "隐私声明",
      home: "首页 ↗",
      top: "回到顶部 ↑"
    },
    sponsor: "赞助",
    license: "仓库尚未声明全局许可证。"
  },

  sponsor: {
    eyebrow: "赞助",
    title: "支持 MoliSync",
    lead: "MoliSync 始终免费、自托管。赞助不会解锁任何功能，也不会改变服务端上报的内容。",
    copy: "复制",
    copied: "已复制",
    btc: "Bitcoin · BTC",
    tron: "TRON · TRC20 USDT",
    bsc: "BNB Smart Chain · BEP20 USDT",
    warning: "请使用上表对应的网络与资产，转错网络将永久丢失；地址请以本仓库为准，勿轻信 fork 或第三方页面。",
    close: "关闭"
  },

  privacy: {
    eyebrow: "隐私",
    title: "隐私声明",
    updated: "最后更新：2026 年 10 月 23 日",
    lead: "MoliSync 是自托管的。你的文件、文字与剪贴板历史永远不会到我们这里；本站没有统计脚本、没有 Cookie，也没有任何第三方请求。",
    what: {
      h: "收集哪些信息",
      body: "<p>每台设备只有一次：第一次连接到任一 MoliSync 服务端时，之后不再上报。事件包含：</p><ul><li>一个不透明 ID，由「部署 ID + 盐化设备键」经 SHA-256 截断到 128 位得到；</li><li>平台（<code>windows</code>、<code>android</code> 或 <code>other</code>）；</li><li>服务端版本；</li><li>时间戳。</li></ul>"
    },
    not: {
      h: "永远不会收集",
      body: "<ul><li>设备名、证书指纹、IP 地址、硬件标识；</li><li>文字内容、文件名、文件内容、剪贴板历史、记录元数据；</li><li>Cookie、广告 ID、指纹脚本、任何统计 SDK；</li><li>账号、邮箱或联系方式。</li></ul>"
    },
    why: {
      h: "用途",
      body: "只用于了解有多少台设备安装了 MoliSync、正在使用哪些版本。不做其他用途。"
    },
    where: {
      h: "数据去向",
      body: "<p>由你自己的服务端发送到 <code>statistics-api.molirain.com</code>（Cloudflare Worker + D1）。</p><p>计数器只保存那个不透明 ID、平台、版本和时间戳。该 ID 限定在单个部署内，因此同一台设备无法在不同部署之间被关联。对外只公布聚合数字。</p><p>Cloudflare 作为基础设施提供商，可能按其自身政策处理请求元数据（例如连接 IP）；计数器代码本身不保存 IP。</p>"
    },
    off: {
      h: "如何关闭",
      body: "<p>设置 <code>MOLISYNC_TELEMETRY_ENABLED=false</code>，或把 <code>telemetry.endpoint</code> 留空即可。同步、P2P 与中继功能完全不受影响。</p><p>每套服务端也提供本地统计 <code>GET /api/v1/stats</code>，这些数据不会离开你的部署。</p>"
    },
    self: {
      h: "数据留在你这一侧",
      body: "<p>文字记录与文件存放在你自己的 SQLite 数据库和 blob 目录里，项目作者无法访问。</p><p>客户端的证书私钥保存在操作系统证书库或 Android KeyChain 中，从不上传。</p>"
    },
    third: {
      h: "第三方请求",
      body: "<p>升级提示会读取公开的 GitHub Releases API。它使用独立的 HTTP 客户端、不携带你的 MoliSync 客户端证书，因此你的设备身份不会交给 GitHub；GitHub 看到的请求与普通访客无异。</p><p>本站是纯静态页面：字体、样式与脚本都从本域名加载。</p>"
    },
    changes: {
      h: "变更与联系",
      body: "本页如有实质变更会更新日期。有疑问或需要删除数据，请在 <a href=\"https://github.com/Molirain/Moli_Sync/issues\" target=\"_blank\" rel=\"noopener\">github.com/Molirain/Moli_Sync/issues</a> 提问。"
    }
  }
});

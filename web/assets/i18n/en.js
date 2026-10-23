window.MoliSyncI18n.add("en", {
  meta: {
    title: "MoliSync — Self-hosted cross-device sync",
    description: "Self-hosted cross-device transfer and sync. mTLS identity, 300 ms LAN/VPN candidate races, and a zero-disk relay at 1.2 s.",
    ogDescription: "mTLS identity, 49-byte frames, and a zero-disk relay. One Go control plane, Windows and Android clients."
  },

  a11y: {
    skip: "Skip to content",
    langSwitch: "Language"
  },

  nav: {
    features: "Features",
    deploy: "Deploy",
    docs: "Docs",
    privacy: "Privacy",
    github: "GitHub"
  },

  hero: {
    kicker: {
      selfHosted: "Self-hosted",
      mtls: "mTLS identity",
      p2p: "P2P streaming"
    },
    title: "Sync across devices.<br><span class=\"text-accent\">On your own network.</span>",
    lead: "One Go control plane, Windows and Android clients. The data path probes LAN/VPN candidates every 300 ms; if nothing answers by 1.2 s, the server relays the session with zero disk writes. Identity is the SHA-256 fingerprint of a client certificate verified at your proxy.",
    cmd: {
      tabs: "Install method",
      copy: "Copy",
      copied: "Copied",
      note: "Docker 24+ · run Compose from the repo root · :9520 stays private"
    },
    facts: {
      arch: "linux/amd64 · arm64",
      runtime: "nonroot · read-only",
      releases: "Releases ↗"
    },
    diagram: {
      heading: "Data path",
      range: "300 ms steps · 1.2 s budget",
      alt: "Sequence diagram: the proxy/control plane issues a single-use 32-byte ticket to both clients. They race LAN and VPN candidates in 300 ms steps, complete mTLS inside a 1.2 s budget, then stream 49-byte frames. If no direct link answers, io.Copy relays the session with zero disk writes.",
      unit: "ms",
      control: "PROXY / CONTROL",
      direct: "DIRECT",
      mtls: "mTLS · SHA-256",
      ticket: "ticket · 32 B",
      race: "LAN / VPN race · 300 ms",
      candidate: "candidate race · 300 ms steps",
      frames: "49 B",
      framesLong: "49 B frames",
      relay: "RELAY",
      fallback: "1.2 s",
      deadline: "1.2 s deadline",
      budget: "≤ 1.2 s",
      noDirect: "no direct link",
      relayCopy: "io.Copy relay",
      zeroDisk: "0 B to disk",
      ticketNote: "single-use 32 B ticket",
      pairing: "15 s pairing timeout",
      caption: "direct first · relay at 1.2 s · no disk writes"
    },
    path: {
      tabs: "Data path mode",
      tabDirect: "Direct P2P",
      tabRelay: "Relay fallback",
      statusDirect: "LAN/VPN · Happy Eyeballs",
      connected: "CONNECTED",
      timeoutShort: "TIMEOUT > 1.2 S",
      serverPipe: "SERVER PIPE",
      ticket: "Session Ticket · 32 B",
      idle: "idle",
      happy: "Happy Eyeballs · 300 ms",
      directStream: "Direct Stream · WireGuard/LAN",
      zeroServer: "0 Server Load",
      frames: "49 B frames",
      directNote: "end-to-end TLS · no server bytes",
      timeout: "LAN/VPN Timeout (> 1.2 s)",
      encrypted: "encrypted stream",
      proxyRelay: "PROXY · RELAY",
      relayBox: "io.Copy RingBuffer",
      relayDisk: "0 B to Disk",
      e2e: "End-to-End Encrypted via Server Pipe",
      captionDirect: "direct path · 0 server load · end-to-end TLS",
      captionRelay: "timeout > 1.2 s · io.Copy relay · 0 B to disk",
      altDirect: "Direct P2P sequence: the proxy issues a session ticket to both clients, Windows probes Android with Happy Eyeballs for 300 ms, then both clients stream over a direct green TLS pipe while the proxy stays idle.",
      altRelay: "Relay sequence: the LAN/VPN probe times out after 1.2 s. Windows sends an encrypted stream into the proxy's io.Copy ring buffer and out to Android; the server writes zero bytes to disk."
    }
  },

  features: {
    eyebrow: "Capabilities / 06",
    title: "What the protocol actually does.",
    lead: "No dashboard demo. Each capability below maps to the wire format, a client behavior or a deployment default.",
    items: [
      {
        tag: "Auth",
        title: "Certificate identity, not shared secrets.",
        body: "The control plane derives DeviceID from the SHA-256 fingerprint of a client certificate verified at the reverse proxy. Inbound identity headers are stripped and rewritten.",
        metric: "SHA-256 · trusted CIDRs"
      },
      {
        tag: "Routing",
        title: "Happy Eyeballs with a hard deadline.",
        body: "Both peers advertise LAN and VPN candidates; the receiver races them in 300 ms steps. If nothing answers by 1.2 s, the session moves to the relay.",
        metric: "300 ms · 1.2 s"
      },
      {
        tag: "Relay",
        title: "Zero disk writes.",
        body: "The server pairs both peers, checks the single-use 32-byte ticket and forwards bytes with io.Copy. Pairing times out after 15 s.",
        metric: "0 B to disk · 15 s"
      },
      {
        tag: "Framing",
        title: "49-byte frames, resumable reads.",
        body: "The data plane uses fixed 49-byte frames; Android adds BLAKE3 digests. Blob downloads support HTTP Range on both clients.",
        metric: "49 B · BLAKE3 · Range"
      },
      {
        tag: "Records",
        title: "Durable until the sender deletes.",
        body: "Text up to 64 KiB lives in SQLite; images and files stream into a blob directory. Records do not expire automatically; only the sender can delete one.",
        metric: "64 KiB · owner-only delete"
      },
      {
        tag: "Runtime",
        title: "Distroless, nonroot, multi-arch.",
        body: "The server image runs on linux/amd64 and linux/arm64 as nonroot with a read-only root filesystem, no shell and no package manager.",
        metric: "amd64 · arm64 · read-only"
      }
    ]
  },

  spec: {
    items: [
      { label: "Text cap", value: "64", unit: "KiB", note: "per record, stored in SQLite" },
      { label: "P2P threshold", value: "15", unit: "MiB", note: "default; 5–50 MiB adjustable" },
      { label: "Relay deadline", value: "1.2", unit: "s", note: "300 ms candidate steps" },
      { label: "Data frame", value: "49", unit: "B", note: "fixed size, both clients" }
    ]
  },

  docs: {
    eyebrow: "Documentation",
    title: "Deploy it behind your proxy.",
    lead: "The example Compose file never publishes port 9520. Terminate mTLS at the proxy, strip inbound X-Client-* headers, then set fresh verified values.",
    server: "Server setup",
    windows: "Windows client",
    android: "Android client"
  },

  footer: {
    tagline: "Self-hosted cross-device transfer and sync. Your server, your certificates, your network path.",
    project: "Project",
    docs: "Docs",
    links: {
      source: "Source code",
      releases: "Releases",
      protocol: "Protocol definition",
      whitepaper: "Architecture whitepaper",
      server: "Server setup",
      windows: "Windows client",
      android: "Android client",
      issues: "Issues",
      privacy: "Privacy",
      home: "Home ↗",
      top: "Back to top ↑"
    },
    sponsor: "Sponsor",
    license: "No repository-wide license has been declared yet."
  },

  sponsor: {
    eyebrow: "Sponsor",
    title: "Support MoliSync",
    lead: "MoliSync stays free and self-hosted. Donations never unlock features and never change what the server sends.",
    copy: "Copy",
    copied: "Copied",
    btc: "Bitcoin · BTC",
    tron: "TRON · TRC20 USDT",
    bsc: "BNB Smart Chain · BEP20 USDT",
    warning: "Send only the listed asset on the listed network; a wrong network loses the funds permanently. Verify the address against this repository, not a fork.",
    close: "Close"
  },

  privacy: {
    eyebrow: "Privacy",
    title: "Privacy statement",
    updated: "Last updated 23 October 2026",
    lead: "MoliSync is self-hosted. Your files, text and clipboard history never reach us, and this website has no analytics, no cookies and no third-party requests.",
    what: {
      h: "What is collected",
      body: "<p>Exactly one event per device, the first time it connects to a MoliSync server, and never again afterwards. It contains:</p><ul><li>an opaque id derived from the deployment id and a salted device key (SHA-256, truncated to 128 bits);</li><li>the platform (<code>windows</code>, <code>android</code> or <code>other</code>);</li><li>the server version;</li><li>a timestamp.</li></ul>"
    },
    not: {
      h: "What is never collected",
      body: "<ul><li>no device name, certificate fingerprint, IP address or hardware identifier;</li><li>no text, file names, file contents, clipboard history or record metadata;</li><li>no cookies, advertising ids, fingerprinting scripts or analytics SDKs;</li><li>no account, e-mail address or contact information.</li></ul>"
    },
    why: {
      h: "Why it exists",
      body: "To know how many devices installed MoliSync and which versions are in use. Nothing else."
    },
    where: {
      h: "Where it goes",
      body: "<p>Your own server sends the event to <code>statistics-api.molirain.com</code>, a Cloudflare Worker backed by D1.</p><p>The counter stores only that opaque id, the platform, the version and the timestamp. The id is scoped to a single deployment, so the same device cannot be linked across installations. Only aggregate numbers are ever published.</p><p>Cloudflare is the infrastructure provider and may process request metadata such as the connecting IP under its own policies; the counter code itself does not store it.</p>"
    },
    off: {
      h: "How to turn it off",
      body: "<p>Set <code>MOLISYNC_TELEMETRY_ENABLED=false</code>, or leave <code>telemetry.endpoint</code> empty. Sync, P2P and relay keep working exactly the same.</p><p>Each server also exposes its own local counters at <code>GET /api/v1/stats</code>; those never leave your deployment.</p>"
    },
    self: {
      h: "Your data stays on your side",
      body: "<p>Text records and files are stored in your SQLite database and blob directory. The project maintainer has no access to them.</p><p>Clients keep certificate private keys in the operating system store or Android KeyChain; they are never uploaded.</p>"
    },
    third: {
      h: "Third-party requests",
      body: "<p>Update checks read the public GitHub Releases API. They use a separate HTTP client without your MoliSync certificate, so your device identity is never presented to GitHub; GitHub simply sees a request like any other visitor.</p><p>This website is static and self-contained: fonts, styles and scripts are served from this domain only.</p>"
    },
    changes: {
      h: "Changes and contact",
      body: "Material changes are recorded here with a new date. Questions or deletion requests: open an issue at <a href=\"https://github.com/Molirain/Moli_Sync/issues\" target=\"_blank\" rel=\"noopener\">github.com/Molirain/Moli_Sync/issues</a>."
    }
  }
});

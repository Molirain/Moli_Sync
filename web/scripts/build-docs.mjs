import fs from "node:fs";
import path from "node:path";

const root = process.cwd();
const docsSrc = path.join(root, "docs-src");
const docsOut = path.join(root, "docs");
const i18nOut = path.join(root, "assets", "i18n");

const pages = [
  {
    slug: "overview",
    file: "index.html",
    group: "start",
    nav: { en: "Overview", zh: "总览" },
    title: { en: "Overview", zh: "总览" },
    docTitle: { en: "MoliSync documentation", zh: "MoliSync 文档" },
    desc: {
      en: "Self-hosted cross-device transfer and sync: architecture, deployment, clients and protocol.",
      zh: "自托管跨端传输与同步：架构、部署、客户端与协议。"
    }
  },
  {
    slug: "server",
    file: "server.html",
    group: "start",
    nav: { en: "Server", zh: "服务端" },
    title: { en: "Server deployment", zh: "部署服务端" },
    docTitle: { en: "Server deployment — MoliSync Docs", zh: "部署服务端 — MoliSync 文档" },
    desc: {
      en: "Run the Go control plane behind a private network: image, Compose, environment variables, health and backups.",
      zh: "在私有网络后运行 Go 控制面：镜像、Compose、环境变量、健康检查与备份。"
    }
  },
  {
    slug: "proxy",
    file: "proxy.html",
    group: "start",
    nav: { en: "Reverse proxy", zh: "反向代理" },
    title: { en: "Reverse proxy & mTLS", zh: "反向代理与 mTLS" },
    docTitle: { en: "Reverse proxy & mTLS — MoliSync Docs", zh: "反向代理与 mTLS — MoliSync 文档" },
    desc: {
      en: "Terminate mTLS, strip identity headers and keep the trusted proxy boundary narrow.",
      zh: "终止 mTLS、清除身份头，并把受信代理边界收窄。"
    }
  },
  {
    slug: "clients",
    file: "clients.html",
    group: "clients",
    nav: { en: "Choose a client", zh: "选择客户端" },
    title: { en: "Clients", zh: "客户端" },
    docTitle: { en: "Clients — MoliSync Docs", zh: "客户端 — MoliSync 文档" },
    desc: {
      en: "Choose the Windows (.NET 8) or Android (Kotlin) client and follow its build and connection steps.",
      zh: "选择 Windows（.NET 8）或 Android（Kotlin）客户端，查看构建与连接步骤。"
    }
  },
  {
    slug: "windows",
    file: "windows.html",
    group: "clients",
    nav: { en: "Windows", zh: "Windows" },
    title: { en: "Windows client", zh: "Windows 客户端" },
    docTitle: { en: "Windows client — MoliSync Docs", zh: "Windows 客户端 — MoliSync 文档" },
    desc: {
      en: "Build, publish with NativeAOT and connect the .NET 8 Windows client.",
      zh: "构建、使用 NativeAOT 发布并连接 .NET 8 Windows 客户端。"
    }
  },
  {
    slug: "android",
    file: "android.html",
    group: "clients",
    nav: { en: "Android", zh: "Android" },
    title: { en: "Android client", zh: "Android 客户端" },
    docTitle: { en: "Android client — MoliSync Docs", zh: "Android 客户端 — MoliSync 文档" },
    desc: {
      en: "Build the Kotlin Android client, configure KeyChain certificates and MediaStore archiving.",
      zh: "构建 Kotlin Android 客户端，配置 KeyChain 证书与 MediaStore 归档。"
    }
  },
  {
    slug: "protocol",
    file: "protocol.html",
    group: "reference",
    nav: { en: "Protocol", zh: "协议" },
    title: { en: "Protocol & data plane", zh: "协议与数据面" },
    docTitle: { en: "Protocol & data plane — MoliSync Docs", zh: "协议与数据面 — MoliSync 文档" },
    desc: {
      en: "Control-plane endpoints, single-use tickets, 300 ms candidate races, 49-byte frames and the zero-disk relay.",
      zh: "控制面端点、一次性票据、300 ms 候选探测、49 字节帧与零落盘中继。"
    }
  },
  {
    slug: "operations",
    file: "operations.html",
    group: "reference",
    nav: { en: "Operations", zh: "运维" },
    title: { en: "Operations & security", zh: "运维与安全" },
    docTitle: { en: "Operations & security — MoliSync Docs", zh: "运维与安全 — MoliSync 文档" },
    desc: {
      en: "Backups, logs, hardening, upgrades and the current limits of MoliSync.",
      zh: "备份、日志、加固、升级，以及 MoliSync 当前的限制。"
    }
  }
];

const groups = [
  { id: "start", en: "Get started", zh: "开始使用" },
  { id: "clients", en: "Clients", zh: "客户端" },
  { id: "reference", en: "Reference", zh: "参考" }
];

const ui = {
  en: {
    docs: "Documentation",
    menu: "Documentation menu",
    onThisPage: "On this page",
    previous: "Previous",
    next: "Next",
    copy: "Copy",
    copied: "Copied",
    pagination: "Pagination",
    home: "Home ↗"
  },
  zh: {
    docs: "文档",
    menu: "文档目录",
    onThisPage: "本页内容",
    previous: "上一篇",
    next: "下一篇",
    copy: "复制",
    copied: "已复制",
    pagination: "分页",
    home: "首页 ↗"
  }
};

function readBody(slug, lang) {
  const file = path.join(docsSrc, `${slug}.${lang}.html`);
  if (!fs.existsSync(file)) throw new Error(`Missing docs source: ${file}`);
  return fs.readFileSync(file, "utf8").trim();
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function buildManual(lang) {
  const langKey = lang === "zh-CN" ? "zh" : lang;
  const manual = {
    ui: ui[langKey],
    group: Object.fromEntries(groups.map((group) => [group.id, group[langKey]])),
    nav: Object.fromEntries(pages.map((page) => [page.slug, page.nav[langKey]])),
    pages: {}
  };

  for (const page of pages) {
    manual.pages[page.slug] = {
      title: page.title[langKey],
      docTitle: page.docTitle[langKey],
      desc: page.desc[langKey],
      body: readBody(page.slug, lang)
    };
  }
  return manual;
}

function writeLocale(code, lang) {
  const manual = buildManual(lang);
  const out = path.join(i18nOut, `docs-${code}.js`);
  fs.writeFileSync(
    out,
    `window.MoliSyncI18n.add(${JSON.stringify(code)}, ${JSON.stringify({ manual }, null, 2)});\n`,
    "utf8"
  );
  console.log(`wrote ${path.relative(root, out)}`);
}

function sidebar(activeSlug) {
  return groups
    .map((group) => {
      const links = pages
        .filter((page) => page.group === group.id)
        .map((page) => {
          const current = page.slug === activeSlug ? ' aria-current="page"' : "";
          return `<a class="docs-link" href="./${page.file}"${current} data-i18n="manual.nav.${page.slug}">${escapeHtml(page.nav.en)}</a>`;
        })
        .join("\n          ");
      return `<div>
        <p class="mono-label mb-2" data-i18n="manual.group.${group.id}">${escapeHtml(group.en)}</p>
        ${links}
      </div>`;
    })
    .join("\n      ");
}

function pagination(index) {
  const prev = pages[index - 1];
  const next = pages[index + 1];
  const card = (page, direction) => {
    if (!page) return "<span></span>";
    const alignment = direction === "next" ? " sm:text-right" : "";
    return `<a class="border border-line px-4 py-3 transition-colors duration-150 ease-out hover:bg-panel${alignment}" href="./${page.file}">
            <span class="mono-label" data-i18n="manual.ui.${direction}">${direction === "previous" ? "Previous" : "Next"}</span>
            <span class="mt-1 block text-[13px] text-fg" data-i18n="manual.nav.${page.slug}">${escapeHtml(page.nav.en)}</span>
          </a>`;
  };
  return `<nav class="mt-14 grid gap-3 border-t border-line pt-6 sm:grid-cols-2" aria-label="Pagination" data-i18n-attr="aria-label:manual.ui.pagination">
          ${card(prev, "previous")}
          ${card(next, "next")}
        </nav>`;
}

function header() {
  return `<header class="sticky top-0 z-40 border-b border-line bg-ink">
    <div class="mx-auto flex h-14 max-w-page items-center gap-6 px-5 sm:px-8">
      <a class="flex shrink-0 items-center gap-2.5" href="../index.html" aria-label="MoliSync">
        <img class="h-6 w-6 rounded-[3px] border border-line" src="../assets/branding/molisync-icon.svg" width="24" height="24" alt="">
        <span class="text-[13px] font-medium tracking-[-0.01em]">MoliSync</span>
      </a>
      <nav class="hidden items-center gap-1 md:flex" aria-label="Primary">
        <a class="px-2 py-1 text-[12px] text-muted transition-colors duration-150 ease-out hover:text-fg" href="../index.html#features" data-i18n="nav.features">Features</a>
        <a class="px-2 py-1 text-[12px] text-muted transition-colors duration-150 ease-out hover:text-fg" href="../index.html#deploy" data-i18n="nav.deploy">Deploy</a>
        <a class="px-2 py-1 text-[12px] text-fg" href="./index.html" data-i18n="nav.docs" aria-current="page">Docs</a>
      </nav>
      <div class="ml-auto flex items-center gap-3">
        <div class="flex items-center" id="lang-switch" role="group" aria-label="Language"></div>
        <a class="hidden h-7 items-center gap-1 border border-line px-2.5 text-[11px] text-fg transition-colors duration-150 ease-out hover:border-muted sm:inline-flex" href="https://github.com/Molirain/Moli_Sync" target="_blank" rel="noopener">
          <span data-i18n="nav.github">GitHub</span><span class="text-muted" aria-hidden="true">↗</span>
        </a>
      </div>
    </div>
  </header>`;
}

function footer() {
  return `<footer class="border-t border-line">
    <div class="mx-auto flex max-w-page flex-col gap-2 px-5 py-6 font-mono text-[9px] uppercase tracking-[0.08em] text-faint sm:flex-row sm:items-center sm:justify-between sm:px-8">
      <span>© <span id="year">2026</span> MoliSync</span>
      <span data-i18n="footer.license">No repository-wide license has been declared yet.</span>
      <a class="link-quiet hover:text-accent" href="../privacy.html" data-i18n="footer.links.privacy">Privacy</a>
      <a class="link-quiet hover:text-accent" href="../index.html" data-i18n="manual.ui.home">Home ↗</a>
    </div>
  </footer>`;
}

function buildPage(page, index, bodyEn) {
  const nav = sidebar(page.slug);
  return `<!doctype html>
<html lang="en" data-title-key="manual.pages.${page.slug}.docTitle" data-description-key="manual.pages.${page.slug}.desc">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${escapeHtml(page.docTitle.en)}</title>
  <meta name="description" content="${escapeHtml(page.desc.en)}">
  <meta name="color-scheme" content="dark">
  <meta name="theme-color" content="#202425">
  <meta property="og:type" content="article">
  <meta property="og:title" content="${escapeHtml(page.docTitle.en)}">
  <meta property="og:description" content="${escapeHtml(page.desc.en)}">
  <meta property="og:image" content="../assets/branding/molisync-icon-512.png">
  <link rel="icon" href="../assets/branding/molisync-icon.svg" type="image/svg+xml">
  <link rel="apple-touch-icon" href="../assets/branding/molisync-icon-512.png">
  <link rel="stylesheet" href="../assets/css/site.css">
  <script defer src="../assets/js/i18n.js"></script>
  <script defer src="../assets/i18n/en.js"></script>
  <script defer src="../assets/i18n/zh-CN.js"></script>
  <script defer src="../assets/i18n/docs-en.js"></script>
  <script defer src="../assets/i18n/docs-zh-CN.js"></script>
</head>
<body id="top">
  <a class="sr-only focus:not-sr-only focus:absolute focus:left-3 focus:top-3 focus:z-50 focus:border focus:border-accent focus:bg-ink focus:px-3 focus:py-2 focus:text-[12px]" href="#main" data-i18n="a11y.skip">Skip to content</a>
  ${header()}

  <main id="main">
    <div class="mx-auto max-w-page px-5 sm:px-8">
      <div class="lg:grid lg:grid-cols-[220px_minmax(0,1fr)] xl:grid-cols-[220px_minmax(0,1fr)_200px]">
        <aside class="hidden border-r border-line py-10 pr-6 lg:sticky lg:top-14 lg:block lg:max-h-[calc(100vh-3.5rem)] lg:self-start lg:overflow-y-auto">
          <p class="mono-label mb-4" data-i18n="manual.ui.docs">Documentation</p>
          <nav class="space-y-7" aria-label="Documentation" data-i18n-attr="aria-label:manual.ui.docs">
            ${nav}
          </nav>
        </aside>

        <div class="min-w-0 py-10 lg:px-10">
          <details class="mb-8 border border-line lg:hidden">
            <summary class="cursor-pointer px-3 py-2.5 font-mono text-[10px] uppercase tracking-[0.08em] text-muted" data-i18n="manual.ui.menu">Documentation menu</summary>
            <div class="border-t border-line px-3 py-3">
              <nav class="grid grid-cols-2 gap-5" aria-label="Documentation" data-i18n-attr="aria-label:manual.ui.docs">
                ${nav}
              </nav>
            </div>
          </details>

          <div class="flex flex-wrap items-center gap-1.5 font-mono text-[9.5px] uppercase tracking-[0.08em] text-faint">
            <a class="transition-colors duration-150 ease-out hover:text-fg" href="./index.html" data-i18n="manual.ui.docs">Documentation</a>
            <span class="text-line" aria-hidden="true">/</span>
            <span data-i18n="manual.nav.${page.slug}">${escapeHtml(page.nav.en)}</span>
          </div>

          <h1 class="mt-4 text-[30px] font-medium leading-tight tracking-[-0.03em] text-fg" data-i18n="manual.pages.${page.slug}.title">${escapeHtml(page.title.en)}</h1>
          <div class="docs-prose mt-6" data-i18n-html="manual.pages.${page.slug}.body">${bodyEn}</div>

          ${pagination(index)}
        </div>

        <aside class="hidden border-l border-line py-10 pl-6 xl:sticky xl:top-14 xl:block xl:max-h-[calc(100vh-3.5rem)] xl:self-start xl:overflow-y-auto">
          <p class="mono-label" data-i18n="manual.ui.onThisPage">On this page</p>
          <nav id="page-toc" class="mt-3" aria-label="On this page" data-i18n-attr="aria-label:manual.ui.onThisPage"></nav>
        </aside>
      </div>
    </div>
  </main>

  ${footer()}

  <script defer src="../assets/js/main.js"></script>
  <script defer src="../assets/js/docs.js"></script>
</body>
</html>
`;
}

fs.mkdirSync(docsOut, { recursive: true });
writeLocale("en", "en");
writeLocale("zh-CN", "zh-CN");

pages.forEach((page, index) => {
  const bodyEn = readBody(page.slug, "en");
  const out = path.join(docsOut, page.file);
  fs.writeFileSync(out, buildPage(page, index, bodyEn), "utf8");
  console.log(`wrote ${path.relative(root, out)}`);
});

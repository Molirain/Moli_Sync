# MoliSync 项目网页

深色、低装饰、以工程指标为主的 landing page，以及一套多页文档站。Tailwind CSS 3.4 构建，原生 JavaScript 处理复制、命令切换、侧边栏目录与中英双语，无框架、无运行时依赖、无第三方请求。

## 技术栈

- **Tailwind CSS 3.4**：源文件 `src/tailwind.css`，构建到 `assets/css/site.css`。
- **自托管字体**：Inter（可变字重）与 JetBrains Mono，放在 `assets/fonts/`，不依赖 Google Fonts。
- **原生 i18n**：`assets/js/i18n.js` 负责检测、切换和字典应用；语言包是普通 JS 脚本，因此 `file://` 下也能工作。
- **无动画依赖**：只有 150ms 的颜色/边框过渡，没有滚动入场或装饰性动效。

## 结构

```text
web/
├── index.html              # Landing page 结构与英文回退文案
├── privacy.html            # 隐私声明（双语，独立页面）
├── docs/                   # 多页文档站（构建产物，已提交）
│   ├── index.html
│   ├── server.html
│   ├── proxy.html
│   ├── clients.html
│   ├── windows.html
│   ├── android.html
│   ├── protocol.html
│   └── operations.html
├── docs-src/               # 文档正文源文件，按语言拆分
│   ├── server.en.html
│   ├── server.zh-CN.html
│   └── …
├── scripts/
│   └── build-docs.mjs      # 生成 docs/*.html 与 docs-*.js 字典
├── _headers                # Cloudflare Pages 安全头与缓存
├── robots.txt
├── package.json
├── package-lock.json
├── tailwind.config.js      # 颜色、字体、宽度 token
├── src/
│   └── tailwind.css        # @font-face、base、docs 与组件类
└── assets/
    ├── css/site.css        # 构建产物（已提交，可直接托管）
    ├── branding/           # 与客户端共用的图标
    ├── fonts/              # Inter / JetBrains Mono + OFL 许可证
    ├── js/
    │   ├── i18n.js
    │   ├── main.js
    │   └── docs.js         # TOC、代码复制、滚动高亮
    └── i18n/
        ├── en.js
        ├── zh-CN.js
        ├── docs-en.js      # 由 build-docs.mjs 生成
        └── docs-zh-CN.js
```

## 本地开发

```sh
cd web
npm ci
npm run build
python -m http.server 4173
# http://localhost:4173/
```

修改 `index.html`、`docs-src/`、`src/tailwind.css` 或 `tailwind.config.js` 后需要重新运行 `npm run build`。开发时可使用 `npm run dev` 持续构建 CSS；只改文档正文时用 `npm run docs` 重新生成 HTML 与语言字典。

不要用 `file://` 预览字体：部分浏览器会因 CORS 拒绝加载本地 woff2，页面会回退到系统字体。用本地 HTTP 服务即可看到真实效果。

## 部署到 Cloudflare Pages

Git 集成（推荐）：

| 设置项 | 值 |
| --- | --- |
| Root directory | 仓库根目录 |
| Build command | `cd web && npm ci && npm run build` |
| Build output directory | `web` |
| Framework preset | None |

也可以把 Root directory 设为 `web`，此时：

| 设置项 | 值 |
| --- | --- |
| Build command | `npm ci && npm run build` |
| Build output directory | `.` |

不想在 CI 中构建时，可以直接使用已提交的 `assets/css/site.css`、`docs/*.html` 与 `docs-*.js`，Build command 留空。只要 HTML 里新增了 Tailwind 类或改动了 `docs-src/`，就必须在本地重新构建并提交产物。

`_headers` 已包含：

- 严格的 CSP：`script-src 'self'`、`style-src 'self'`、`font-src 'self'`，不允许内联脚本与内联样式。
- `nosniff`、`X-Frame-Options: DENY`、`frame-ancestors 'none'`。
- HTML 不缓存，`assets/*` 缓存一天。

站点不需要 Cloudflare Functions 或环境变量。

## 视觉约束

继续扩展页面时请保持这些规则，避免回到通用 AI 模板风格：

- 底色 `#202425`，面板 `#171A1B` / `#242829`，描边 `#343A3B`；唯一强调色 `#28CCC9`。
- 圆角只允许 0–4px；不要引入 `rounded-lg` 以上的卡片。
- 层次只用背景深浅和 1px 细线，不使用投影、玻璃拟态、渐变或发光。
- 标题和正文用 Inter；命令、端点、指标、时间轴标签用 JetBrains Mono。
- 过渡 150ms、`ease-out`；不要添加滚动入场、视差或循环动画。
- 文案写具体指标与行为，避免 "seamless"、"next-gen" 一类空词。

设计 token 在 `tailwind.config.js` 与 `src/tailwind.css`；改色或改字体只需要改这两处。

## 文档站

`docs/` 下是 8 个独立页面，共用 landing 的 header 与 token：

| 页面 | 内容 |
| --- | --- |
| `index.html` | 总览、组件职责、传输流程、当前限制 |
| `server.html` | 镜像、Compose、环境变量、健康检查、数据卷 |
| `proxy.html` | 信任顺序、Nginx 示例、头映射、网络边界 |
| `clients.html` | 客户端总览、共同行为与两条构建入口 |
| `windows.html` | Windows 客户端的构建、证书与能力 |
| `android.html` | Android 客户端的构建、证书与能力 |
| `protocol.html` | HTTP 端点、P2P 会话、帧与 blob |
| `operations.html` | 备份、日志、加固、升级、已知限制 |

布局是 Docker 文档式的三栏：左侧分组导航、中间正文、右侧本页目录。`xl` 以下隐藏右栏，`lg` 以下把左栏收进原生 `<details>` 菜单。

文档正文在 `docs-src/<slug>.en.html` / `.zh-CN.html`，由 `scripts/build-docs.mjs` 生成为：

- `docs/*.html`：英文正文内联，作为无 JS 回退；页面标题与描述通过 `data-title-key` / `data-description-key` 切换。
- `assets/i18n/docs-en.js` / `docs-zh-CN.js`：把每种语言的完整正文放进 `manual.pages.<slug>.body`。切换语言时整段替换，因此同一时间只有一个版本的标题 ID，目录锚点不会重复。

新增文档页：

1. 在 `scripts/build-docs.mjs` 的 `pages` 中加一项（slug、文件名、分组、中英标题与描述）。
2. 新建 `docs-src/<slug>.en.html` 与 `<slug>.zh-CN.html`，用 `docs-lead`、`h2 id="…"`、`table`、`<pre data-lang="sh">`、`callout` 等结构。
3. 运行 `npm run build`。

`docs.js` 会自动做三件事：从 `h2[id]` / `h3[id]` 生成右侧目录并做滚动高亮、把每个 `<pre>` 包成带复制按钮的代码块、在语言切换后重新执行。正文标题必须带 `id`，否则不会进入目录。

## 多语言

加载优先级：`?lang=xx` → `localStorage` 中的手动选择 → `navigator.languages` → 英文。手动切换会写入 `?lang=` 和 `localStorage`。

新增语言（例如法语）：

1. 复制 `assets/i18n/en.js` 为 `assets/i18n/fr.js`，把 `MoliSyncI18n.add("en", …)` 改成 `"fr"` 并翻译。
2. 在 `assets/js/i18n.js` 的 `LOCALES` 中加一项：

   ```js
   { code: "fr", label: "FR", name: "Français", file: "assets/i18n/fr.js", aliases: ["fr-fr", "fr-be"] }
   ```

3. （可选）在 `index.html` 的 `<head>` 中加 `<script defer src="assets/i18n/fr.js"></script>`，让首帧就使用目标语言；不加也会按需加载。
4. 从右到左的语言在 `LOCALES` 项中加 `dir: "rtl"`，`<html dir>` 会自动更新。

文案约定：

| 用法 | 说明 |
| --- | --- |
| `data-i18n="hero.lead"` | 替换 `textContent` |
| `data-i18n-html="hero.title"` | 替换 `innerHTML`，用于带 `<span>` / `<br>` 的标题 |
| `data-i18n-attr="aria-label:hero.diagram.alt"` | 替换指定属性，多个用逗号分隔 |
| 数组 | 用数字键，如 `features.items.0.title` |

`index.html` 始终保留英文原文作为无 JS 回退；缺失的键会回退到英文。

文档站使用两层字典：landing 的 `en.js` / `zh-CN.js` 提供 header、footer 与通用 UI；生成的 `docs-*.js` 提供 `manual.*` 命名空间（导航、分组、页面标题、完整正文）。`i18n.js` 会在字典后加载时自动重新应用一次，因此 docs 字典不必早于运行时加载。

## 维护提示

- 版本号 `v0.4.0` 在首屏 kicker 的方框徽标中；发版时更新。
- 命令框有两个真实安装路径：`Docker` 与 `Compose`。修改命令时同时更新 `data-cmd` 和 `<code id="cmd-line">` 的初始文本；Compose 需要在仓库根目录执行。
- 时序图在 `index.html` 中：`lg` 以上是 SVG 协议时序，`lg` 以下是紧凑列表。改动时间点或文案时两边都要更新，并同步 `assets/i18n/*.js` 中的 `hero.diagram.*`。
- 文档正文只改 `docs-src/`，不要直接改 `docs/*.html`；生成器会覆盖它们。改完运行 `npm run docs` 或 `npm run build`。
- 文档链接指向仓库 `master` 分支；默认分支改名时需要同步修改。
- 字体许可证：`assets/fonts/LICENSE-Inter.txt` 与 `assets/fonts/LICENSE-JetBrainsMono.txt`（均为 SIL OFL）。分发字体时请保留这两份文件。

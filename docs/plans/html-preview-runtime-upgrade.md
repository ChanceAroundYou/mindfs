# HTML 预览升级：真正当网页跑（同源沙箱 + 根路径兜底 + 白屏可诊断）

**状态**：Active（2026-11-09 起）。承接 `html-video-preview.md`（G-JP）的预览链路，改的是同一块互斥边界。

## 背景（实测证据，2026-11-09）

在本机 7331 上用 Chromium 直连 `/api/file/raw/...` 复现：

| 场景 | 现象 |
|---|---|
| 沙箱页里访问 `localStorage` | 抛 `SecurityError`；`sessionStorage` 同样；`serviceWorker` 被禁用 |
| 沙箱页里 `<script>` / `<script type="module">` / `fetch('./data.json')` | **能跑**（opaque origin 仍放行同 URL 源的资源请求） |
| `web/index.html`（Vite 开发入口） | 白屏。`/src/main.tsx` 被后端按扩展名回成 `application/x-tiled-tsx`，`nosniff` 下模块脚本被拒；**而且 TSX 浏览器本来也执行不了** |
| `web/dist/index.html` | 能出内容，但 `serviceWorker` 报 `SecurityError` |

结论：拦住「像浏览器一样加载」的主因是 `sandbox="allow-scripts"`（不给 `allow-same-origin`）
和响应头 `CSP: sandbox allow-scripts` 这层隔离；`web/index.html` 另有「开发入口本身不可运行」这一独立原因。
顺带发现系统 mime 表把 `.ts` 认成 `text/vnd.trolltech.linguist`、`.tsx` 认成 `application/x-tiled-tsx`、
`.cjs`/`.jsx` 认成空值。

## 用户决策（2026-11-09）

1. **放开同源**：iframe 与响应头都加 `allow-same-origin`，页面按普通标签页运行。
   已知代价：被预览的 HTML 与 mindfs 同源，能碰 `parent.document`、改应用 UI 与偏好。
   （实测它在隔离模式下本来就能 `fetch` 到 `/mindfs/api/*`，增量风险主要是 UI 篡改而非数据读取。）
2. 同时做三件配套：**开发入口明确提示**、**根绝对路径兜底**、**白屏时显示失败原因**。

## 落地

### 后端（`server/internal/api/`）

| 文件 | 改动 |
|---|---|
| `http_raw_files.go` | HTML 响应头改为 `CSP: sandbox allow-scripts allow-same-origin`；新增 MIME 归一（`.mjs/.cjs` → `text/javascript`，`.ts/.tsx/.jsx` → `text/plain`，不再吐 trolltech/tiled 这类噪音）；HTML 预览读取字节后走改写路径 |
| `raw_asset_html.go`（新） | `rewriteRootAbsoluteHTML`：把 `src="/x"`、`href="/x"`、`poster="/x"` 改写成相对当前文档的 `../…/x`，让站点根落到项目根。跳过：有 `<base>` 的文档、`//` 协议相对、部署前缀（mindfs 自己的 `/mindfs/assets/...`）、`/api`、`/ws`、`?download=1`、>8MB 的文件 |
| `raw_asset_html_test.go`（新） | 钉住改写规则与跳过条件 |

为什么改写而不是别的：浏览器把 `/assets/x.js` 解析到**服务器根**，而预览资源在
`/api/file/raw/{account}/{root}/…` 下；改成相对路径后无论部署前缀怎么变都自然落回项目根。

### 前端（`web/src/`）

| 文件 | 改动 |
|---|---|
| `services/htmlPreview.ts`（新） | 纯函数 `findUnrunnableScripts` / `isDevEntryHtml`：识别 `.ts/.tsx/.jsx/.vue/.svelte` 脚本引用与 `/@vite/`、`/@react-refresh` 开发态注入 |
| `hooks/useHtmlPreviewFrame.ts`（新） | URL 构造 + 开发入口判定 + iframe `load` 后在同源 `contentWindow` 上挂 `error`（捕获阶段，含资源失败）与 `unhandledrejection`，并做「无可见内容」判定 |
| `components/file/HtmlPreview.tsx` | `sandbox="allow-scripts allow-same-origin"`；顶部提示条展示开发入口 / 运行时错误 / 空白原因 |
| `components/file/FileViewer.tsx` | 把 `file.content` 传给 HtmlPreview 做静态体检 |
| `i18n/locales/*-projects.ts` | 三条文案（中英各一） |
| `index.css` | `.document-preview-html` 改纵向 flex，提示条占一行，iframe 占满剩余高度 |

铁律照旧：组件不 import `services/`，判定逻辑经 hook 进入。

## 验收

- `make test`（Go）+ `cd web && npm test` + `npm run typecheck` + `make build-web`。
- 手动：预览 `web/dist/index.html` 能出界面；预览 `web/index.html` 显示「开发入口，请先构建」
  而不是白屏；预览一个引用 `/assets/x.js` 的静态页能加载。
- 回归：`web/tests/media-preview.test.mjs`、`server/internal/api/raw_asset_test.go` 全绿。

## 登记

并入 `docs/upstream-customizations.yaml` 的 **G-JP**（同一互斥边界：HTML/视频预览与原始资源端点），
提交信息标 `Scope: G-JP`。

# HTML 网页与视频文件的浏览

**状态**：Implemented（2026-10-22），已提交（`d6b48253`，`Scope: G-JP`）并快进合入 `main`；待 ship（构建安装 + 分发 WSL/fn）。

## 目标

文件浏览器里点开 `.html` / `.htm` 能像网页一样渲染（相对路径的 CSS/图片/JS 照常加载），
点开 `.mp4` / `.webm` / `.mov` 能直接播放（可拖进度条）。两者都不改动既有图片 / PDF /
Office 预览链路。

## 现状与结论

- 既有原始内容只有一条路：`GET /api/file?raw=1`（`server/internal/api/http_files.go`），
  账户分区靠查询串里的 `user=`（`api.ScopedRouter` + `workspace.go`）。
- **网页里的相对 URL 会丢掉查询串**：`<img src="img/x.png">` 在浏览器里解析成
  `/api/file/raw/xxx/img/x.png`，`user=` 没了 → 掉回主账户取资源。
  所以「完整预览」必须有一条**路径式**的原始资源端点，让账户段进路径。
- 账户 id 每台机器独立、跨机器要靠用户名（`services/base.ts` 的同源判定），
  路径里的账户段同源用 id、跨机器用用户名。
- 视频要能拖进度条 → 后端必须支持 Range（`http.ServeContent` 现成）。

## 落地

### 后端

| 文件 | 作用 |
|---|---|
| `server/internal/api/raw_asset_path.go`（新） | `WithRawAccountPath` 中间件：把路径里的账户段搬进 `user=`，从路径剥掉；必须跑在 `ScopedRouter` **之前** |
| `server/internal/api/http_raw_files.go`（新） | `handleRawAsset`：`usecase.OpenFileRaw` + `http.ServeContent`（Range/206）、`nosniff`、HTML 加 `CSP: sandbox allow-scripts`、`?download=1` 走 attachment |
| `server/internal/api/http_routes.go` | 新增路由 `GET /api/file/raw/{root}/*` |
| `server/app/server.go` | inner mux 上挂 `api.WithRawAccountPath(httpRoutes)` |

端点形态：`/api/file/raw/{account}/{root}/{path...}`。

两个坑：

1. **chi 优先按 `RawPath`（转义态）匹配** → 路由参数会停在转义态，文件名带空格/中文就对不上。
   中间件清掉 `RawPath`，让 chi 用已解码的 `Path` 匹配。
2. **不能二次 `PathUnescape`**：`r.URL.Path` 在 net/http 解析时已解过一次，
   再解会把用户名里的字面 `%41` 解成 `A`。

### 前端

| 文件 | 作用 |
|---|---|
| `web/src/services/mediaPreview.ts`（新） | `isHtmlPreview` / `classifyVideo`（playable / unsupported） |
| `web/src/services/file/rawAsset.ts`（新） | `buildRawAssetURL`：拼路径式 URL，逐段 `encodeURIComponent` |
| `web/src/services/net/base.ts` | 新增 `accountSegmentFor`（同源→账户 id，跨机器→用户名）与 `appBasePath`（不带 `user=` 的 base） |
| `web/src/hooks/useRawAssetUrl.ts`（新） | 组件层不许直接 import services（`reviewer-architecture-rules.test.mjs`），由 hook 兜住 |
| `web/src/components/file/HtmlPreview.tsx`（新） | `sandbox="allow-scripts"`（**不给** `allow-same-origin` → opaque origin）的 iframe |
| `web/src/components/file/VideoPreview.tsx`（新） | `<video controls>` 直连后端端点（边下边播，不占浏览器内存） |
| `web/src/components/file/FileViewer.tsx` / `useFileViewer.ts` | 接线：HTML 默认渲染预览 + 顶栏「源码 / 预览」切换；视频走 VideoPreview |
| `web/src/index.css` | `.document-preview-html` / `.document-preview-video` |

沙箱是两层：iframe `sandbox="allow-scripts"` + 响应头 `CSP: sandbox allow-scripts`。
后者保证**直接在新标签页打开该 URL 也一样隔离**。opaque origin 下页面里的 JS 仍能跑
（用户选的是「允许脚本但隔离来源」）。

## 兼容策略

- 既有调用方**零改动**：`/api/file?raw=1` 原样保留，图片 / PDF / Office 预览仍走它。
- 新端点是**新增路径**，不改老端点语义；无数据迁移。
- nginx：`location ~* ^/mindfs/api(/|$)` 覆盖新路径，没有 `.html` 劫持规则 → 反代后可用。
- API 层仍不做鉴权（仓库既有决策），账户分区靠 `user=` / 路径里的账户段。

## 范围外

- 不做 HTML 的 DOM 编辑 / 源码高亮（源码视图复用既有 CodeViewer）。
- 不做 `.avi` / `.mkv` 等浏览器解不了的格式（给下载提示，不引第三方解码器）。
- 不做视频转码、字幕、封面。

## 验证

- `server/internal/api/raw_asset_test.go`：直取 200、同账户兄弟资源（style.css）、
  Range → 206、路径穿越 → 400、缺文件 → 404、账户段搬进 `user=`、其它路径不受影响。
- `web/tests/media-preview.test.mjs`：类型判定、账户段（同源/跨机器）、URL 编码、
  跨节点 URL、无账户时返回空串。
- `cd web && npm test` → 183 个测试文件 / 370 个用例全绿。
- `cd web && npm run typecheck` → 无错。`make build-web` → 构建通过。`make test` → Go 全绿。

## 提交与登记

- `Scope: G-JP`，登记进 `docs/upstream-customizations.yaml`（新组 G-JP）。

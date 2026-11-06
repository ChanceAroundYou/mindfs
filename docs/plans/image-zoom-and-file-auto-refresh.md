# 图片缩放与文件自动刷新

状态：Implemented 2026-11-06，待 ship。Scope: G-KK（图片缩放）、G-KL（版本化缓存 + 自动刷新）。

## 背景

用户报两件事：

1. **图片看不清、放不大**：会话里 agent 生成的图（截图、架构图、图表）按原始尺寸/容器宽度渲染，
   手机上没有放大手段；关系图（mermaid）早有缩放，图片没有。
2. **同名文件改了还是旧版**：编辑或重新生成同名文件后，再打开文件视图仍是旧内容。

两件事根因不同，但都落在「预览资源」这条链上，所以一轮做完。

## 现状与根因（第二件事）

同一路径拿不到新内容，是四层缓存叠加的结果，缺一层都还是旧版：

| 层 | 位置 | 症状 |
|---|---|---|
| JS blob 缓存 | `services/file/blob.ts` 的 `rawFileBlobCache`，key `rootId:path:nodeId`，**无 TTL** | 同路径永远返回缓存里的旧 Blob |
| React 重取 | `useProtectedBlobUrl` 依赖只有 path/root/nodeId；`ImageViewer`/`DocumentViewer` 的 memo 比较器同理 | 文件变了但请求根本不重发 |
| 组件生命周期 | 选中会话时 `FileViewer` 不渲染，切回来不会重新取 payload | 界面停在切走前那份数据 |
| 浏览器 HTTP 缓存 | `/api/file?raw=1` 与 `/api/file/raw/...` 都没有 `Cache-Control` | 浏览器按启发式缓存长期复用旧字节 |

WS 的 `file.changed` 只覆盖「本节点 + 文件视图可见」这一种情形：跨节点事件到不了浏览器，
切回文件视图时也没人重放。

## 方案

### 图片缩放（G-KK）

- **数学单源**：`shared/zoomTransform.ts`（`zoomAt` / `gestureMetrics` / 0.25–16 夹取）。
  图表预览原本自带一份，已改为经 `shared/diagramZoom.ts` 门面转出（旧名字 `zoomDiagramAt` /
  `diagramGesture` 保留，`DiagramPreview.tsx` 与既有测试不动）。
- **手势**：`hooks/usePinchZoom.ts` —— 双指缩放、单指拖动、双击在「适配 ↔ 2 倍」切换；
  滚轮**只**在 Ctrl/⌘ 时接管（裸滚轮属于页面滚动）。
- **两个消费面**（用户选定）：
  - 文件视图 `ImageViewer`：内联缩放，未放大时 `touch-action: pan-y`（页面照常滚），
    放大后独占；右上角工具栏 −/%/+/全屏。
  - 会话图片 `MarkdownImage`：点击开全屏灯箱。
  - 附件（`AttachmentImage`）**不做**（用户明确排除）。
- **灯箱**：`components/common/ImageZoomOverlay.tsx`，`<dialog showModal>` + portal，
  Escape/关闭按钮收场、锁 body 滚动、关闭后还焦点；结构与 `DiagramPreview` 一致，
  fit 按图片自然尺寸算（图表那边是 SVG viewBox，取数方式不同，所以只共用数学不复用组件）。

### 版本化缓存 + 自动刷新（G-KL）

- **版本 = 文件的 `mtime`**（后端 `/api/file` 已返回，且本来就用于 304 校验），
  `revision` 兜底。用它做缓存键而不是新增接口字段：WS 事件送不到也不影响正确性。
- 版本贯穿五处：blob Map key → 请求 URL `?v=` → hook 依赖 → memo 比较器 → 服务端缓存头。
- **三条刷新通道**（用户选定「事件驱动 + 轮询 + 手动按钮」，范围「当前文件 + 所在目录」）：
  1. 既有 WS `file.changed`（主路径，不动）；
  2. `app/useFileAutoRefresh.ts`：仅文件视图且无选中会话时，5s 轮询 + 页面重新可见/聚焦立即补一轮；
  3. 文件视图顶栏刷新按钮：`force` 绕过前端缓存与 mtime 304，直接取磁盘现状。
  变化时顺带刷新文件所在目录（大小/存在性跟着变）。
- `refreshCurrentFileContent` 改为返回 `Promise<boolean>` 并支持 `{force}`；**内容没变不 setFile**
  （否则轮询每轮换新对象，预览重渲染、选区/滚动抖）。

## 兼容策略

- `diagramZoom` 旧 API 全家保留，图表预览零改动；`diagram-zoom.test.mjs` 未动仍绿。
- `fetchProofProtectedBlob` / `buildRawAssetURL` / `useProtectedBlobUrl` / `useRawAssetUrl`
  的 `version` 是**可选末尾参数**，旧调用点不传即旧行为。
- `refreshCurrentFileContent` 的 `{force}` 可选，返回值从 `void` 变 `boolean`（`Promise<void>`
  兼容）；相关类型声明（`useAppDomainLayerTypes` / `useRealtimeEventsContext` /
  `useAppViewCompositionTypes`）同步。
- 新增 i18n key 中英各一份；无新依赖（缩放数学手写，手势用 Pointer Events）。
- 服务端只加两个响应头：路径式 `Cache-Control: no-cache`（保留 Last-Modified，304 很便宜），
  查询式 `no-store`（前端自己按版本缓存）。接口、参数、数据格式不变。

## 改动文件

新增：
- `web/src/shared/zoomTransform.ts`
- `web/src/hooks/usePinchZoom.ts`
- `web/src/components/common/ImageZoomOverlay.tsx`
- `web/src/app/useFileAutoRefresh.ts`
- `server/internal/api/raw_file_cache_test.go`
- `web/tests/zoom-transform.test.mjs`、`image-zoom-ui.test.mjs`、
  `file-blob-versioning.test.mjs`、`file-auto-refresh.test.mjs`
- `e2e/test_image_zoom_and_file_refresh.py`（隔离实例验收）

修改（图片）：`shared/diagramZoom.ts`、`components/common/MarkdownImage.tsx`、
`components/file/ImageViewer.tsx`、`components/file/FileViewer.tsx`、两份 locale。

修改（刷新）：`services/file/blob.ts`、`services/file/rawAsset.ts`、`hooks/useProtectedBlobUrl.ts`、
`hooks/useRawAssetUrl.ts`、`components/file/{DocumentViewer,useDocumentPreviewBlob,VideoPreview,HtmlPreview,useFileViewer}.tsx|ts`、
`components/file/{icons,FileTreeRefreshButton}.tsx`、`components/file/FileViewer.tsx`、
`app/{useFileRefresh,useAppViewComposition,useAppViewCompositionTypes,useAppDomainLayerTypes,useRealtimeEventsContext,AppRootView}.ts(x)`、
`components/app/{sessionPaneTypes.ts,SessionMainContent.tsx}`、
`server/internal/api/{http_files,http_raw_files}.go`、两份 locale、
`web/tests/document-preview-view-split.test.mjs`（断言 `useDocumentPreviewBlob(root, path, version)`）。

## 验证

- `cd web && npm run typecheck` — 通过
- `cd web && npm test` — 206 文件 / 465 通过（5 skipped）
- `make build-web` — 通过
- `make test`（Go 全量）— 通过
- `go test ./server/internal/api -run TestRawFileResponsesDisableHeuristicCaching` — 通过
  （真跑 handler，断言两个端点各自的 Cache-Control）
- `E2E_SKIP_BUILD=1 .venv/bin/python -m pytest e2e/ -q` — 13 passed（10 既有 + 3 新增）
  - 新增 `e2e/test_image_zoom_and_file_refresh.py` 在隔离实例 + 无头 Chromium 上验了三条
    用户路径：内联缩放/全屏灯箱（Escape 收场）；**改写同名图片后重新打开 `naturalWidth`
    从 64 变 96**（即原 bug 的复现路径，版本链没漏层）；打开状态下改写图片，不点任何按钮
    自己换成 128px。
- 收尾复核（yaml / 方案文档定稿后重跑）：`npm run typecheck`、vitest 全量 206 文件 / 465 通过、
  `make build-web`、`make test`、新增 e2e 3 passed、`TestRawFileResponsesDisableHeuristicCaching` 全绿。
- `make check-upstream` — 提交前必红，且只红 8 条：G-KK/G-KL 登记的新文件（`zoomTransform` /
  `usePinchZoom` / `ImageZoomOverlay` / `useFileAutoRefresh` / e2e / 方案文档）尚未提交，
  不在 `merge-base..HEAD` 的 delta 里。它只看**已提交**的文件，提交后自动转绿；
  其余 delta 文件（locale、FileViewer 等）早已随前序提交进 delta，不再报。
- 触摸手势（双指缩放、拖动）仍未上真机点过 —— 无头浏览器只验到按钮/键盘路径，待 ship 后验收。

## 与原方案的差异

1. **灯箱 vs 内联的分工**：内联保留触摸拖动（未放大时 `pan-y` 放行页面滚动），
   不做「点图开灯箱」而用显式全屏按钮 —— 单击与拖动/双击手势会打架。
2. **未加图片 `onError` 重试**：重新 set 同一个 blob URL 救不回解码失败的图；
   真问题由 1s 去抖的 WS 事件与 5s 轮询覆盖。
3. **`FileTreeRefreshButton` 顺手改用共享 `RefreshIcon`**：新增的顶栏刷新按钮需要同一个图标，
   避免图标 path 抄两份。
4. **图片预览改为满高**：原先图片在 `padding` 容器里按自然尺寸渲染、由外层滚动，
   缩放语义下外层该不滚（否则拖动会连带滚动），所以归入 `fullHeightPreview`。

# MindFS 上游定制清单与评估（改动级互斥）

> **当前基准：`9ab9572`（tag `v0.5.5`）= `git merge-base HEAD upstream/main`。**
> 核对口径：`git diff 9ab9572..HEAD --name-status` = **380 文件**（新增 213 / 修改 140 / 删除 27）。
> **机器真相在 `docs/upstream-customizations.yaml`** —— 分组 id、文件、针对性测试、锚点全部以它为准，本文不重复列举；
> 覆盖率门禁 `make check-upstream` 强制「每个 delta 文件都被某组覆盖」。两文件的唯一强耦合点是**组 id 双向一致**。
>
> 本文档 §1-§3 分组为 v0.4.7→v0.4.9 时代沉淀，机制仍适用；v0.5.1 / 7757ca8 合并记录见 §0，v0.5.5 见 §0-B，接回 main 见 §0-B-2。
>
> 历史口径（2026-08-26 首次成文时的记录，**仅作沿革参考，不再是核对基准**）: 基准 `upstream/main` tag `v0.4.7` `18b10ca`，
> 对比 `HEAD = 0591238`，`git log --reverse 18b10ca..HEAD` 44 提交，`git diff 18b10ca...HEAD` 110 文件 `+8333/-4249`。
> 分组原则: **按 hunk 归类**——同一提交、同一文件不同行可归不同组；每行改动仅属一组，组间互斥、全体完备。
> 判定: `git show --numstat/--stat` 逐提交核验 + 逐 hunk 归类，`git cherry -v` 校验上游等价。

---

## 0-B. v0.5.5 合并记录（2026-09-30）

- **Merge commit `976a5d0`**（双 parent：上游 `9ab9572` × 本地 `bb4244d`），merge-base `v0.5.2`(`7757ca8`) → **`bb4244d`**。
  上游 25 commits / 125 files，本地 311 commits / 304 files，重叠 39 files；合并相对本地 +5400 / −488（95 files）。
- 12 个前端冲突文件 / 51 段，**逐 hunk 定解，无 `--theirs` 整文件**（重文件铁律）：
  `App.tsx` 25 · `ActionBar.tsx` 11 · `SessionViewer.tsx` 3 · `services/tasks.ts` 2 · `SessionList.tsx` 2 ·
  `useSessionStream.ts` / `TaskTemplateDialog.tsx` / `FileViewer.tsx` / `DefaultListView.tsx` /
  `CodexRateLimitIndicator.tsx` / `i18n en-US` / `i18n zh-CN` 各 1。
- **架构级取舍：拒绝上游 task_groups 编排体系**（用户决策）。上游给看板加了 `task_groups` 分组 + 并发调度器 +
  `scheduler_admitted` 准入位；本地早已按 CLAUDE.md 事实 13 重做为「卡=任务、列=全局状态、无并发调度」。
  连带丢弃：`kanban.Start` 调度器、`TaskGroupPanel`/`TaskCardText` 组件、`patchTask`/`fetchTaskGroups`/`groupOperation`
  等 API、`GET/PATCH/DELETE /api/tasks/{id}` 与 `/read/{resource}` 路由、`openTaskEditDialog`（本地 `b3ced22` 已主动删）。
  `SchedulerAdmitted` 字段保留为 DB 列兼容位（无调度器时恒 true），非调度逻辑。
- **App.tsx 逐段定解**（22 段机械可判 + 2 段用户决策）：
  - H02/H13（1308 + 1519 行）取 local：这两大段是上游的 module-level helper 与一个巨型 effect，本地早把它们抽进
    `web/src/app/`（19 个模块）。逐个核对 84 个 helper：上游对本区间真正改过的只有 3 个且**函数体与 base 逐字相同**、
    改动全在调用点；唯一新增的 `taskAgentStage` 只服务上游 taskGroup 路径，本地无该概念。
  - H06/H08/H09/H10/H11/H14（输入会话归属）取 local 的 `resolveLockedSessionKey` 版本，只嫁接上游的
    `pending-` 前缀守卫——本地版本带会话锁（`session-lock.test.mjs` 守着），换上游会破锁。
  - H16（533 行看板渲染）取 local `TaskBoardView`；H24（弹窗外壳）取 local `PanelShell`（带未保存改动确认），
    但**保留上游的 worktree 新分支名输入框**（H25）。
  - H19/H20 上游 `FileViewer`/`DefaultListView` 新 props（`onFileOperationComplete`/`editStore`/`onFileUpdated`）
    与本地 `rootDisplayName`/`rootColor` 并存。
- **本轮新接上游能力**：`services/quickSwitch.ts`（蓝环手势判定）嫁接进本地 `SessionRing`——本地环已有左滑新建 +
  主题色 + 抽屉感知，补上「上滑拉出快捷切换面板」；配套删掉上游独立的 `SessionQuickActions.tsx`，
  新增 `web/tests/session-ring-gesture.test.mjs` 守住接线（`ringGesture` 不许被组件内联阈值取代）。
- **修复上游带进来的系统弹窗**：`App.tsx` `handleCreateBlankFile` 与新文件 `FileOperations.tsx` 用了
  `window.prompt/alert/confirm`，违反本地 `no-system-dialogs.test.mjs`；全部换成 `promptDialog`/`alertDialog`/`confirmDialog`。
- **其它保留的上游修复**：`manager.go` exchange seqno 去重、`cli/cmd/mindfs.go` 的 `resolveClientTLS` 与 3 处
  `addrToURL` bug（IPv6 通配地址 / JoinHostPort）、`SessionList` 子会话改名、`GetSession` 返回值适配、
  5 个上游回归测试。
- 新增前端依赖：`@codemirror/*`（编辑器）、`@lezer/highlight`、`@tanstack/react-virtual`。
- 门禁全绿：`go build`/`go vet`/`go test ./server/... ./cli/...`、`tsc --noEmit` 0 err、`node --test tests/*.test.mjs` **106/106**。
  `make build` 的 Go link 步在未提交的 merge worktree 里会因 VCS stamping 失败（`error obtaining VCS status`），
  提交后自愈；`go build -buildvcs=false` 已验证二进制可正常链接。
- 部署：见 §部署记录；本机 `systemctl restart mindfs` 由用户执行。

### 0-B-2. 接回 main：`e3974c6`（2026-09-30）

- 合并期间 main 前进 5 个提交（`31bc465` worktree 失效检测 + `704349d` gofmt + `8f3d480` 立即执行落段，
  经 `b097521` / `da1af39` 两次 merge），`976a5d0` 的第一 parent 落在 25 提交前的上游侧，
  故 §5 的 `--ff-only` 接回不可行。改为在 merge worktree 内 `git merge da1af39` 再接回。
- **零冲突**（`git merge-tree --write-tree` 干跑预判，20 文件全自动并入）。两个方向的冲突集都是空集。
- **一次误判的记录**：接回前曾把上游 parent `9ab9572` 误读为「main 的旧快照」，据此判定
  `--ff-only` 必败并阻塞。实际 `9ab9572` 就是 tag `v0.5.5` 本身（作者 `yandc`），是合并的**上游父**；
  真正让 `--ff-only` 失效的只是 main 前进这 5 个提交，与 parent 身份无关。判 parent 身份须用
  `git log --format=%an` / `git tag --points-at`，不能靠 commit 数猜。
- 门禁复跑全绿：`go build -buildvcs=false` / `go vet` / `go test ./...`（全 ok）、
  `tsc --noEmit` 0 err、web **111/111**（上游带入 5 个新测试）、全仓冲突标记扫描为空。
  `v0.5.5` 与 `da1af39` 均为 `e3974c6` 祖先；本地定制（ScopedRouter / per-account AppContext /
  repoint / 看板阶段快照）与两份多账户分区契约测试均在位。
- gofmt 复核：`e3974c6` 有 9 个文件未格式化，**全部为 main 既有**（`da1af39` 同样 9 个），
  合并未引入新违规，反而少 1 个（`api/appcontext.go`）。非本轮引入，未在此轮顺手格式化（避免混入无关 diff）。
- 本机 `main` 上的两个脏文件（`usecase/session.go` / `usecase_test.go`）是用户未提交的工作：
  `BuildPromptInput.LinesBeforeThisTurn` 由 `int` 改 `*int`，修「首轮真实值恰为 0 被当哨兵、
  误判为跨会话迁移」；与上游对同文件的 55 行新增（DeleteSession 级联 / MindFS context 注入 /
  `currentAssistantLine` 裁剪）无重叠。已单独验证其绿（`TestPrependSwitchHintIsNotInjectedOnFirstTurn` PASS），
  **未纳入本次合并**，仍留在 main 工作区待用户提交。

---

## 0-A. 7757ca8 合并记录（2026-09-22）

- **Merge commit `8897fb9`**（双 parent：本地 `b6af0fc` × 上游 `7757ca8`），merge-base `v0.5.1`(`4f0c762`) → **`7757ca8`**。上游 11 commits / 21 files。
- **仅 3 个冲突文件 / 5 段**（上一轮是 13 文件），全部逐 hunk 并集（**无 `--theirs`**）。
- 上游主题：provider 一键模型同步 + 单模型测试 + 模型搜索（PR #94）、ACP token 计费修正（PR #95）、外部绝对路径文件读取（修 outside-file 404）。
- 关键取舍：
  - `App.tsx`：**删除上游 relay 登录/节点重定向块**（本地 G-H 已整体裁剪 Relay，无调用点）；保留上游 `normalizePathForRoot` 迁往 `services/fileNavigation.ts` 的重构（本地仍在用），并去掉随块而来的 `shouldRedirectToRelayNodes` import。
  - `AgentSelector.tsx`：并集——上游 `AGENT_MODEL_SEARCH_THRESHOLD` 与本地 Claude 别名 helper（`strip1MSuffix`/`isClaudeAliasModelName`/`claudeModelBase`）是同点独立新增，两者都留。
  - `FileTree.tsx`：3 段并集——`import React, { memo }` + 上游 `ProviderModelSelect`；`openSessionNaming` 保留上游 `agentConfigFlowVersion`/`setAgentConfigNotice`；保留上游 `changeProviderTestModel`，**丢弃 relay 提示轮播块**，`childKeyFor` 取本地 `sectionNodeId`+`treeKey` 版本。
- 自动合并的重叠文件已逐一语义复核（上游内容全部在位）：`http.go`（sync-all/test 路由与本地 users/nodes 路由并存）、`usecase/fs.go`（上游 `resolveFileReadTarget` 在 90-320 行、本地 `UpdateRootDisplayName` 在 752-1010 行，互不重叠）、`preferences/store.go`（`AgentLastConfigSelections` 纯新增）、i18n zh/en（上游 3 个模型搜索键与本地多账户键并存）。
- 反向对账（`git diff HEAD..7757ca8 -- <file>`）：21 个上游文件中 13 个为空（完整吸收），8 个差异恰为本地定制所在文件。
- 门禁全绿：go build/vet、tsc 0 err、node tests 35/35、`make build`+`make install`；`go test ./...` 绿。
  `server/internal/kanban` 的 flake（`runner exec count=2` + `readonly database (1032)`）经查是
  **真 bug**（同一任务的 agent 阶段被并发执行两次），已由 `d90a4c7` 修复，非合并引入。
- 部署：VM `make install` 完成（`v0.5.1-167-g0b0ef82`），待用户重启；WSL 见部署记录。

---

## 0. v0.5.1 合并记录（2026-09-10）

- **Merge commit `56d130f`**（双 parent：本地 `5841704` × 上游 `4f0c762`），merge-base `v0.4.9` → `v0.5.1`（`b06dab6`）。上游 21 commits / 69 files。
- 13 个冲突文件逐 hunk 并集（**无 `--theirs`**），本地定制 132 files `+12527/-3865`（`git diff upstream/main...HEAD`）。
- 关键取舍：
  - `App.tsx`：onboarding mainContentView 并集 + `scopedRootKey` 化；上游 11 props 全保留但删 `multiProjectSessionsEnabled`/`onMultiProjectSessionsChange` 两 props（本地硬编码 `const multiProjectSessionsEnabled = true`，无 setter）。
  - `FileTree.tsx`：6 hunks 全并集（fontSizePreferences/sendShortcut/node 菜单/prefetch/双菜单按钮）；丢弃 `relayServicesPopoverRef` 与 `setRelayServicesOpen`（上游 Relay 服务入口，本地已裁剪 G-H）。
  - `SessionViewer.tsx`：assistant meta 区取上游侧 + 完整 copy 按钮 + ✓ span 补回 `themeColor`（本地主题色透传）。
  - `session.ts`：probe 计数器统一上游命名 `consecutiveProbeFailures`。
  - `prefix.go`：`OriginalPath` 走 `r.URL.EscapedPath()`（E2EE 签名证明与转义路径一致）。
- 测试正则适配本地约定（保功能）：`main-content-view-memory.test.mjs`（scopedRootKey）、`session-window.test.mjs`（`consecutiveProbeFailures` 字段名）。
- 门禁全绿：go vet/test、tsc 0 err、node tests 27/27、make build；`mergeWindowedTail` 红线无回归。
- 部署：VM `make install` 完成待用户重启；WSL rsync+restart 已生效（v0.5.1-82-g56d130f）。

---

## 1. 总览（36 组）

| 组 | 主题 | 性质 | 关键提交/切片举例 | 互斥边界 |
|----|------|------|-------------------|----------|
| G-A | 文档与发布说明 | 文档 | `5600d4e/c6fba4e/13365dc/4af6f89/0be192c` | 仅 `docs/**`、`release-notes.md` |
| G-B | HTTP 语义修正（404/空树/静态资源） | 修复 | `ab8999f:http.go#handleTree/handleFile`、`7a12971:http.go#stripMindfsPrefix/pathForStaticAsset` | 后端路由/错误码语义 |
| G-C | Markdown/Mermaid/文件服务 | 修复/体验 | `ab8999f:MarkdownViewer/file.ts`、`2e85356:file.ts` | 前端渲染与缓存 |
| G-D | 刷新旋转动效 | 体验 | `040fbd2` 全量 | `FileTree/ActionBar` 的刷新动效与 `useRefreshSpin` |
| G-E | 会话存储后端性能 | 性能 | `2e85356:manager.go/fs/external_sessions`、`7837c28:WAL` | 仅后端存储/索引/并发 |
| G-F | 前端流式与渲染性能 | 性能 | `2e85356:App/FileTree/SessionList/renderer`、`df445bd` 全量 | 仅前端渲染/防抖/缓存 |
| G-G | 会话生命周期与 WS 正确性 | 修复 | `bd52027/8bc5a43/7f8a8c6/9df43e9` | 流式标记/中断/同步 |
| G-H | 旧功能裁剪（公网/Relay/TokenStation） | 裁剪 | `df732af` 全量 | 删除文件与入口 |
| G-I | 安全模型重塑（e2ee/鉴权/CORS） | 破坏性 | `c7c2c45:http.go#protectedEndpoint/cors` + `e2ee.ts/storage/connection/api/bootstrap` 切片、`2d814b2:bootstrap/e2ee` 兼容 | 鉴权/加密/跨域 |
| G-J | 多节点聚合与路由 | 架构 | `c7c2c45:nodeRegistry/NodeSwitcher/runtime` 切片、`2d814b2:nodeRegistry/runtime` 切片、`7a12971:session/git/tasks/rootNode` 切片、`3d3417a:scope/App/session` 切片 | 节点注册/聚合/按 nodeId 路由 |
| G-K | 部署前缀与代理基础设施 | 架构 | `ecb2ad8/306f588/e92fecf` 的 `deploy/prefix`/`api/prefix`/`http_nodes`/`ws`/`vite`/`Makefile` 切片 | 前缀规范化与反代 |
| G-L | 视觉与主题体系 | 视觉 | `7a12971:NodeBadgeHeader/index.css/useNodeRegistry` 切片、`b2c209b/e28ce08/25f0001/481da95:ModeIcon/ModeSelector/FileTree` 主题色切片 | 颜色/徽标/深色模式 |
| G-M | 子路径 ServiceWorker | 部署 | `6eb9457` 全量 | 仅 SW 与构建戳 |
| G-N | 会话锁定 | 交互 | `9b531f9/e26c3ac/1436053` | `sessionLock` 与 `App.tsx` 锁定 hunk |
| G-O | 可拖动浮层 | 交互 | `955241a/76d586c/727bf8f/28deb2a/1765353/e459a13:bottomSheetModel` 切片 + 未提交 `BottomSheet.tsx` 切片 | `BottomSheet` 拖拽/阈值 |
| G-P | 项目别名（display_name） | 数据模型 | `481da95` 主体 + `4c59fcf:manager` 切片、`e459a13:manager` 切片 | `registry/display_name` 与广播 |
| G-Q | 会话别名/外部导入/Fork 扁平 | 数据模型 | `e459a13:SessionList` 切片、`f746124/4c59fcf/ab8cfed` | `fork→独立`/`agent+session_id→alias` |
| G-R | 模型识别与流式透传 | 修复 | `af0a37f` 主体、`ab8cfed:probe` 1M 切片、`e92fecf:AgentSelector` 切片 | DeepSeek/Claude 1M 探测与 `stream_hub/ws` 透传 |
| G-S | 作用域隔离与列表折叠 | 修复 | `3d3417a/ed44573/0591238` + `7a12971:App` 聚合切片 | `scope.ts`、`expanded/loadMore` 按 `nodeId:projectId` |
| G-T | 流式重放与正文正确性 | 修复 | 见 §3「G-T」—— `session.ts`/`useSessionStreamCache.ts`/`useRealtimeEvents.ts`/`App.tsx`/`stream_hub.go`/`ws_test.go` | 重放幂等、seq=0 只在在途时存在、重放整批一条消息 |
| G-U | 多账户分区（多配置档） | 架构 | 见 §3.1 | `ScopedRouter` + 每账户 `AppContext` + 分账户 `MetaDir` |
| G-V | 控制面 / 数据面分离 | 架构 | 见 §3.1 | 控制面只在主节点；`nodeinfo.Role` 决定 |
| G-W | worktree 会话 repoint | 修复 | 见 §3.1 | 换 id/推进游标/清归属的固定顺序 |
| G-X | 置顶（pins） | 功能 | 见 §3.1 | 权威在主节点、按账户一份、键不带 node id |
| G-Y | 看板模型重做（拒绝上游 task_groups） | 裁剪 | 见 §3.1 | 无并发调度；任务自带流水快照 |
| G-Z | 跨项目工作台 | 功能 | 见 §3.1 | `overview` 汇总 + 按卡片 `root_id` 派发 |
| G-AA | 前端 App.tsx 模块化重构 | 架构 | 见 §3.1 | `web/src/app/*` 19 模块 + `renderer/*` |
| G-AB | 会话投影与审计 | 修复 | 见 §3.1 | `session_projection` / `audit` |
| G-AC | ACP 运行时 + agents.json 用户层 | 修复 | 见 §3.1 | 只读用户层再合并 |
| G-AD | 会话列表 / 树 / 归档 / 搜索 | 功能 | 见 §3.1 | 合并·建树·分组均为纯函数 |
| G-AE | UI 基础设施（自建对话框） | 修复 | 见 §3.1 | 禁 `window.prompt/alert/confirm` |
| G-AG | 输入区与编辑器 | 功能 | 见 §3.1 | `action/*` + CodeMirror token 编辑器 |
| G-AH | 文件树 / Git 视图 / 文档预览 | 功能 | 见 §3.1 | 请求带 `nodeId`；根视图三页签 |
| G-AI | PWA / 陈旧资源自愈 / 子路径 SW | 修复 | 见 §3.1 | 原 G-M 并入 |
| G-AJ | 偏好 / 定时任务 / 提示词 / 更新 / WebPush | 功能 | 见 §3.1 | 共享 vs 按账户的边界见事实 12 |
| G-AK | 本地化（中英双语键） | 功能 | 见 §3.1 | 合上游最易被整文件覆盖 |
| G-AL | 构建 / 部署 / 收尾流程脚本 | 部署 | 见 §3.1 | `ship` / `upstream-merge` skill |
| G-AM | 插件注册表与视图目录 | 功能 | 见 §3.1 | 主视图记忆依赖它 |

> **G-D 已并入 G-F，G-M 已并入 G-AI**（改动面完全重合、无独立测试可守，单列只会制造空组）。

> 同一提交跨组示例见 §3 表中「跨组拆分」列；同一文件跨组示例：`App.tsx` 按 hunk 分属 G-F/G-G/G-J/G-L/G-N/G-S 等 6 组，`http.go` 按 hunk 分属 G-B/G-I/G-K 三组。

---

## 2. 基准与分歧

- `18b10ca` 后 `upstream/main` 走到 `ce52160`（`v0.4.9`）新增 28 提交，与本地分叉（`merge-base --is-ancestor upstream/main HEAD` 为假）。
- `git cherry -v` 44 提交均为 `+`，但 `ab8999f≈d6cc497`、`040fbd2≈0338765` 语义等价（先行修复），合并时为文本冲突。
- 上游独有：`869de8a` deepseek harness、`9ef14ac` CodeBuddy、`466b21e` `~/.mindfs`、`d36cc53` 重连续传等；本地独有：WAL/增量/流式节流、e2ee 移除/CORS、节点聚合/前缀、别名/fork 等，属架构分歧需 rebase 而非 fast-forward。

---

## 3. 改动级互斥清单

### G-A 文档与发布（5 提交，纯文档）

| 提交 | 文件 | 改动 |
|------|------|------|
| `5600d4e` | `release-notes.md` | 发布说明 |
| `c6fba4e` | `docs/superpowers/plans/2026-08-21-node-colors-project-header-workspace-preferences.md` + `specs/...design.md` | 节点主题色方案沉淀 |
| `13365dc/4af6f89/0be192c` | `docs/superpowers/specs/2026-08-22-session-lock-and-draggable-chat-sheet-design.md` + `plans/...sheet.md` | 锁定与浮层设计/计划 |

### G-B HTTP 语义修正

| 来源 | 文件:行/函数 | 归属 | 说明 |
|------|--------------|------|------|
| `ab8999f` | `server/internal/api/http.go:handleTree/handleFile` | G-B | 目录不存在→200 空树；文件不存在→404（原 400）；按 `os.ErrNotExist` 分流 |
| `7a12971` | `server/internal/api/http.go:stripMindfsPrefix/pathForStaticAsset/handleNotFound` | G-B | `/mindfs` 前缀剥离、`pathForStaticAsset` 的 `mindfs/assets` 兼容、`NotFound/MethodNotAllowed` 统一 |
| `306f588` | `server/internal/api/http_test.go` 相关 | G-B | 上述路由的回归用例 |

> 与 G-I/G-K 的 `http.go` hunk 互斥：`protectedEndpoint/corsMiddleware` 归 G-I，`Routes().Use(prefix)` 归 G-K，此处仅错误码与静态资源。

### G-C Markdown/Mermaid/文件服务

| 来源 | 文件 | 说明 |
|------|------|------|
| `ab8999f` | `web/src/components/MarkdownViewer.tsx` | mermaid `\|` 含裸管道时加引号（11.14.0 怪癖） |
| `ab8999f/2e85356` | `web/src/services/file.ts` | `raw 404` 60s 缓存、成功缓存与并发去重、`@json-render` 懒加载 |
| `2e85356` | `web/src/components/MarkdownViewer.tsx` | 复用缓存后的渲染路径 |

> **G-D 已并入 G-F**（刷新旋转动效 `040fbd2`：`useRefreshSpin(450ms)` 统一 `FileTree` 与任务面板旋转、`SyncIcon` 支持 `style`）。
> 改动面（`FileTree.tsx` / `App.tsx` / `useRefreshSpin.ts`）与 G-F 完全重合，且没有独立测试可守，单列只会制造空组。

### G-E 会话存储后端性能

| 来源 | 文件 | 说明 |
|------|------|------|
| `2e85356` | `server/internal/session/manager.go` | `exchanges/aux` JSONL 游标增量、列表 `meta-only` + `agent bindings IN` 单查、`updated_at/pinned_at` 索引 |
| `2e85356` | `server/internal/fs/fs.go`、`server/internal/api/usecase/external_sessions.go` | `SyncExternalSessionDelta` 2s 节流、`handleSessionGet` 去重复扫盘 |
| `7837c28` | `server/internal/session/manager.go:openSessionMetaDB` + `GetMeta` + `http.go:replying-sessions` | `WAL+busy_timeout+SetMaxOpenConns(4)`、`GetMeta` 不读 JSONL、活跃轮询走 meta |

### G-F 前端流式与渲染性能

| 来源 | 文件 | 说明 |
|------|------|------|
| `2e85356` | `web/src/App.tsx`（WS 300ms debounce、`multiProjectSessions` 聚合）、`web/src/components/FileTree.tsx/SessionList.tsx/MarkdownViewer.tsx`（memo）、`web/src/renderer/*` | WS 重拉防抖、`SessionCard/FileTree/MarkdownCodeBlock` memo、`onSelect` 稳定化、懒加载拆包 |
| `df445bd` | `web/src/App.tsx:cacheVersion 30ms+markSessionPending 幂等` | 流式 100/s→~33/s，删 7 处 `updateDrawerIfShowingStream` 冗余 setState |
| `df445bd` | `web/src/hooks/useSessionStream.ts:streamVersion 30ms` | 补 `App` 之外的独立 setState 风暴路径 |
| `df445bd` | `web/src/components/MarkdownViewer.tsx:useDeferredValue` | markdown 解析走 React 低优先级，流式不阻塞输入/滚动 |
| `df445bd` | `web/src/services/session.ts:IDB 截断` | 仅存 `meta+最近 500条/200KB`，`truncated→syncSession` 全量补段；`userShell` O(n²)→单累积 text |

> `df445bd` 末尾的 `context_window` 徽标继承与 `session.done→replace` 实为 G-G 语义，但实现落在同提交的 `App.tsx` 同一 hunk，按「主因」归 G-F，评估中单列说明。

### G-G 会话生命周期与 WS 正确性

| 提交 | 文件 | 说明 |
|------|------|------|
| `bd52027` | `web/src/hooks/useSessionStream.ts:3行` | `message_done` 立即清流式标记，修“正在生成”卡住 |
| `8bc5a43` | `server/internal/agent/claude/session.go:Interrupt 3s 超时` + `server/internal/api/ws.go:go handleCancel` | 单读 goroutine 异步派发 + 中断超时，修停止按钮失效 |
| `7f8a8c6` | `web/src/App.tsx` | 会话历史同步滞后（`loadSessions` 时序） |
| `9df43e9` | `web/src/App.tsx:seq 守卫+pending draft` | `loadMultiProjectSessionGroups` 竞态守卫、pending→真实会话替换、多项目实时更新 |
| `af0a37f` | `server/internal/api/stream_hub.go/ws.go` 辅助 hunk | `model_display_name` 重放与 `session.accepted` 乐观缓存（与 G-R 的探测 hunk 分离） |

### G-H 旧功能裁剪

| 提交 | 涉及文件 | 说明 |
|------|----------|------|
| `df732af` | `web/src/components/RelayLocalServicesDialog.tsx`(删)、`web/src/services/relayServices.ts/tokenStation.ts/launcherNodeSync.ts/nativeBridge.ts`、`web/src/App.tsx/FileTree.tsx/Login.tsx/base.ts/bootstrap.ts/i18n` | 移除公网访问/Relay 对话框/Token 加油站三件套 |
| `20f2b90` | `.gitignore` | `CLAUDE.md` 忽略（配置类，不影响运行时，单列但归本组） |

### G-I 安全模型重塑

| 来源与 hunk | 文件 | 说明 |
|-------------|------|------|
| `c7c2c45:http.go#protectedEndpoint` | `server/internal/api/http.go:protectedEndpoint` | 直通（`ponytail: 鉴权/e2ee 已移除`），删 e2ee 解密/Proof 校验 |
| `c7c2c45:http.go#corsMiddleware` | 同文件 `corsMiddleware+Routes.Use` | `*` + `OPTIONS 204` 全开放 |
| `c7c2c45` | `web/src/services/e2ee.ts`、`storage.ts`、`connection.ts`、`api.ts`、`bootstrap.ts` | `e2ee.ts` 659→39 行 stub、去 token、`api.ts` 无鉴权 `fetchJSON`、`bootstrap` 直通 `ready` |
| `2d814b2` | `web/src/services/bootstrap.ts/e2ee.ts` | stub 保留 `e2ee` 字段/方法兼容，防旧订阅 `undefined` 白屏 |

> 与 G-J 的 `runtime.ts/nodeRegistry.ts` 同提交但 hunk 互斥：鉴权相关归本组，路由/聚合归 G-J。

### G-J 多节点聚合与路由

| 来源与 hunk | 文件 | 说明 |
|-------------|------|------|
| `c7c2c45` | `web/src/services/nodeRegistry.ts`（PALETTE/CRUD/聚合开关）、`web/src/components/NodeSwitcher.tsx`、`web/src/hooks/useNodeRegistry.ts`、`web/src/services/runtime.ts/base.ts/main.tsx` | 节点注册表引入、`mindfs_nodes` 存储迁移、`runtime/base` 按 `nodeId` 路由、`main` 登录直注册 |
| `2d814b2` | `web/src/services/nodeRegistry.ts:normalizeNodeURL` + `web/src/services/runtime.ts:prefix 归一` | 裸 origin 节点在 `/mindfs` 页面自动补前缀 |
| `7a12971` | `web/src/services/rootNode.ts`、`session.ts:connect(ws,nodeId)/buildWSUrl`、`git.ts:fetchGit* nodeId`、`agents.ts/candidates.ts/codexRateLimits.ts/download.ts/file.ts/scheduledTasks.ts/tasks.ts/upload.ts`、`web/src/App.tsx:mapManagedRootsToEntries/_nodeId/_nodeColor + getNodeIdForRoot` | `appURL(...,nodeId)` 与 `rootNode` 映射打通跨节点会话/代理/文件/Git |
| `3d3417a` | `web/src/services/scope.ts:scopeKey/treeKey`、`web/src/App.tsx/FileTree.tsx/SessionList.tsx/session.ts` 的 `scopeKey` 全链路 | 按 `nodeId:projectId` 隔离（与 G-S 的折叠 hunk 分离，此处仅键与路由） |

### G-K 部署前缀与代理基础设施

| 来源 | 文件 | 说明 |
|------|------|------|
| `ecb2ad8` | `internal/deploy/prefix.go`、`server/internal/api/prefix.go`、`server/internal/nodes/store.go`、`server/internal/api/http_nodes.go`、`web/src/services/prefix.ts/nodeBase.ts` | 前缀规范化单一事实源（后/前各一）、`nodes/store` 聚合、`http_nodes` 反代、`nodeBase.normalize` |
| `306f588` | `Makefile`、`cli/cmd/mindfs.go`、`scripts/build-all.sh`、`server/internal/api/appcontext.go/http.go/ws.go`、`server/internal/relay/service.go` | 构建与 CLI 前缀收敛、`ws` 透传、`relay` 前缀 |
| `e92fecf` | `web/index.html`、`web/vite.config.ts:base/scope`、`web/src/services/base.ts/runtime.ts` | Vite `base=/mindfs/`、HTML 基路径、前后端基路径收敛、`web/tests/deploy-prefix/node-url-normalize` |

### G-L 视觉与主题体系

| 来源与 hunk | 文件 | 说明 |
|-------------|------|------|
| `7a12971` | `web/src/components/NodeBadgeHeader.tsx`、`web/src/index.css: --node-badge-bg/--node-row-selected-bg`、`web/src/hooks/useNodeRegistry.ts:PALETTE 6色 + 迁移`、`server/app/server.go`（静态资源主题相关） | 徽标底纹中性灰、选中态浅灰长条、低饱和 6 色 |
| `b2c209b/e28ce08/c6fba4e` | `web/src/App.tsx:rootColor 透传`、`web/src/components/DefaultListView.tsx/FileTree.tsx`、`web/src/services/nodeRegistry.ts` + `web/tests/node-colors-and-workspace-preferences.test.mjs` | 固定工作区偏好与主题色透传链 `PALETTE→_nodeColor→rootColor/accent` |
| `7a12971/481da95` | `web/src/components/ActionBar.tsx/ModeIcon.tsx/ModeSelector.tsx/FileTree.tsx:fileTreeHexToRgba/tabAccent` | 主题色经 `rootColor/groupColor` 注入边框/阴影/图标（与 G-P 的别名 hunk 分离） |
| `25f0001` | `web/src/components/DefaultListView.tsx` | `project-home` 徽标与节点徽标样式统一 |

> **G-M 已并入 G-AI**（子路径 ServiceWorker `6eb9457`：`scopeRelativePathname` 剥离 `/mindfs`、去 `?v=` 冗余、构建戳防 SW 死锁，移除 nginx patched SW）。
> 改动面（`registerServiceWorker.ts` / `vite.config.ts:buildToken`）与 G-AI 完全重合，单列只会制造空组。

### G-N 会话锁定

| 提交/hunk | 文件 | 说明 |
|-----------|------|------|
| `9b531f9` | `web/src/services/sessionLock.ts` + `web/tests/session-lock.test.mjs` | `normalizeSessionLockKey` 契约 |
| `e26c3ac/1436053` | `web/src/App.tsx:锁定 hunk` | 浏览内容时会话不丢；移除未使用导入 |

### G-O 可拖动浮层

| 来源/hunk | 文件 | 说明 |
|-----------|------|------|
| `955241a` | `web/src/services/bottomSheetModel.ts` + `web/tests/bottom-sheet-model.test.mjs` | `clampHeight/resolveBottomSheetRelease` 契约 |
| `76d586c/727bf8f/28deb2a/1765353` | `web/src/components/BottomSheet.tsx` | 50% 默认、任意高度悬停、`pointercancel→50%`、触摸防抢占 |
| `e459a13` | `web/src/services/bottomSheetModel.ts:BOTTOM_SHEET_TOP_EDGE_RATIO=0.1` | 上沿 10% 吸顶（与同提交的 `manager/SessionList` hunk 分离） |
| 未提交 | `web/src/components/BottomSheet.tsx` | `rAF` 节流、`70%` 默认、`contain:layout paint`、`willChange`、双击全屏（沿革见 §4） |

### G-P 项目别名（display_name）

| 来源/hunk | 文件 | 说明 |
|-----------|------|------|
| `481da95` | `server/internal/fs/registry.go:UpdateDisplayName` + `server/internal/api/http.go:POST /api/dirs/{id}/display-name` + `server/internal/api/appcontext.go/usecase/fs.go/root.go` + `web/src/App.tsx:getRootDisplayName/currentRootDisplayName` 等 | `ID/RootPath` 不变、`display_name` 落 `registry.json`、`managedDirResponse` 与 `display_name_changed` 广播、面包屑 `✓/×` 编辑 |
| `e459a13` | `server/internal/session/manager.go:display_name 持久化切片` | 别名与会话 `manager` 的重命名持久化（与同提交的 `BottomSheet/SessionList` 切片分离） |
| `4c59fcf` | `server/internal/session/manager.go:alias 回填` | `agent/agent_session_id` 列回填（与 G-Q 的导入标题 hunk 分离） |

### G-Q 会话别名/外部导入/Fork 扁平

| 来源/hunk | 文件 | 说明 |
|-----------|------|------|
| `e459a13` | `web/src/components/SessionList.tsx:fork 扁平切片` | 仅 `fork` 扁平为顶级、其它保持折叠（与同提交的 `manager/bottomSheet` 分离） |
| `f746124` | `web/src/components/SessionList.tsx` | 计数徽标移除、统一会话样式；`fork` 扁平收敛 |
| `4c59fcf` | `server/internal/api/usecase/external_sessions.go:LookupAliasForAgent` + `web/src/components/SessionList.tsx:重命名按钮` | 导入前 `alias` 回放、末条用户消息 20 字短标题、内联 `✓/×` |
| `ab8cfed` | `server/internal/session/manager.go:不再写 parent_session_key` + `server/app/server.go:启动归一化` + `server/internal/api/usecase/session.go` + `server/internal/agent/claude/session.go:external_name` | fork 完全独立、历史原子归一化、关联文件回流、外部名称按 `agent+agent_session_id` 落盘 |

### G-R 模型识别与流式透传

| 来源/hunk | 文件 | 说明 |
|-----------|------|------|
| `ab8cfed` | `server/internal/agent/probe.go` + `server/internal/agent/claude/session_test.go` | Claude 1M 变体探测（与同提交的 fork hunk 分离） |
| `af0a37f` | `server/internal/agent/claude/session.go:claudeEffectiveEnv/ResolveClaudeModelArg/TierKey` + `probe.go:TierKey+SanitizeDefaultModelID` + `server/internal/api/stream_hub.go/ws.go/appcontext.go/preferences/store.go/scheduled/tasks.go` + `web/src/App.tsx/ActionBar.tsx/AgentSelector.tsx` | CC Switch 下 DeepSeek 真实模型分层叠加、`[1m]` 后缀兼容、实时 `model_display_name` 贯通与乐观缓存 |

### G-S 作用域隔离与列表折叠

| 来源/hunk | 文件 | 说明 |
|-----------|------|------|
| `3d3417a` | `web/src/services/scope.ts` + `web/tests/scope-keys.test.mjs` | `scopeKey/scopeSessionKey/treeKey/expandKey` 键基建，空 `nodeId` 兼容旧格式 |
| `3d3417a/ed44573/0591238` | `web/src/components/SessionList.tsx:expandedProjects/groupIsCurrentNode/handleProjectHeaderToggle/topLevelSessionsForGroup/remaining` | 默认展开=本节点、收起全隐藏、`remaining` 按顶层计、`onLoadMoreProject` 节点门控 |
| `3d3417a/0591238` | `web/src/App.tsx:loadMore _nodeId 门控`、`web/src/components/FileTree.tsx/NodeBadgeHeader.tsx:chevron`、`web/vite.config.ts:escLiteral` | 跨节点同名项目不串扰、组头 SVG chevron、构建正则转义 |

---

### G-T 流式重放与正文正确性（2026-10-06）

**互斥边界**：`session.stream` 的重放下发与客户端应用、`seq=0` 瞬时行的生命周期。
**合上游时若与上游冲突，以本组为准** —— 这里的每一条都对应一个已实测的可见缺陷。

| 改动 | 文件 | 为什么必须保留 |
|------|------|----------------|
| `mergeStreamedText(existing, incoming)` 纯函数 | `web/src/services/session.ts` | 流式拼接必须**重放安全**（整段重放→覆盖；重复分片→忽略；否则追加）。原先 `appendAgentChunkForSession` 是**无条件拼接**，而服务端会重推在途内容，导致同一段正文在**行内**被拼成 `aabb` —— 用户看到「文本出现两遍、越切越多」。重复在行内而非两行之间，所以任何按行内容比对的去重都查不出来 |
| 三处拼接改调 `mergeStreamedText` | `web/src/app/useSessionStreamCache.ts`（agent 1 处、thought 2 处） | thought 那份原本自带等价判据、agent 那份没有 —— 三处收敛成一个定义，避免再次分叉 |
| **删掉**加载时 `clearEventCursor` | `web/src/App.tsx` | 光标是「我已收到 X 为止」的凭证，`session.ready` 会带上它；清掉 = 主动要求全量重放 → 「每切一次会话刷一大堆」。而且轮次边界（`session.user_message`/`session.done`）本来就会自动清，这里是多余的 |
| 重放**整批一条消息**下发 | `server/internal/api/stream_hub.go:replayStepToClient` + 新增 `buildSessionStreamBatchResponse` | 原实现 `for i := range events { SendToClient(...&events[i]) }` —— 几百条事件 = 几百条 WS 消息 = 几百次渲染 = 「首次打开运行中会话时逐渐刷出来」。整批一条 → 客户端同一处理器内循环 → React 批处理成一次渲染 |
| 客户端识别批量帧 | `web/src/app/useRealtimeEvents.ts`（`session.stream` 分发） | 认 `payload.events` 数组并循环应用；单条实时事件仍走 `payload.event` |
| `composeLoadedExchanges(server, cached, inFlight)` | `web/src/services/session.ts` + `App.tsx` 两处调用 | 加载时的**唯一**组装规则。瞬时行（seq=0）只在**在途**时保留 —— 服务端从不经窗口回包下发它们，所以绝不能去服务端回包找（曾经三处那么写、三处恒空，导致切回运行中会话时在途内容整块丢失）。另：只保留**最后一次 compact 之后**的瞬时行（compact 是服务端历史的重置点） |
| `dropTransientExchanges` 接进 `session.done` / `compact_notice` | `web/src/services/session.ts` + `web/src/app/useRealtimeEvents.ts` | 补齐**从来不存在**的清理出口。此前 seq=0 无任何清理机制，实测跨 7 次 compact 堆积到 1100+ 条 |

**针对性测试（防覆盖，改动时同步维护）**

| 测试 | 钉住什么 |
|------|---------|
| `web/tests/session-window.test.mjs` → `mergeStreamedText` 7 组 | 增量 / 整段重放 / 重复分片 / **幂等（重放 8 次不增长）** / 逐段重放收敛 / 空输入 |
| 同上 → `composeLoadedExchanges` 8 组 | 冷缓存 / 在途保留全部瞬时行 / **不在途全丢** / compact 边界切分 / 无 compact 不砍 / 同对象去重 / 缓存更全 / 空输入 |
| 同上 → `dropTransientExchanges` | 清空 seq=0；无瞬时行时返回**同一引用**（避免无意义重渲染） |
| `web/tests/upstream-restore.test.mjs` | d36cc53 契约改指真正落点（`session.ready` 带 `event_cursor`），并**反向断言 App 不得出现 `clearEventCursor`** |
| `server/internal/api/ws_test.go` → `TestReplayBatchIsOneMessage` | 重放必须是**一条**带 `events` 数组的消息，且不同时携带单条 `event` |

**验证记录**：CDP 冷启动一个运行中的会话，WS 帧统计 —— 重放 `events[92]` 到达 **1 条**（修复前会是 92 条单事件帧）；观测窗口内另有 25 条单帧为**实时**事件（正常）。
`go build` / `go test ./internal/api/` / `tsc --noEmit` / `node --test tests/*.test.mjs`（145/145）全绿；关键判据均做变异验证（改回旧写法即红）。

---

### 3.1 补全分组（2026-10-06 新增，18 组）

> §3 的 G-A…G-S 是 v0.4.7 时代按 hunk 归的类，只覆盖了当时 68 个文件。
> 2026-10-06 用 `git diff $(git merge-base HEAD upstream/main)..HEAD` 对账，定制面实为 **380 文件**，
> 补出以下 18 组。**每组的文件、测试、锚点、可见症状、为什么必须保留都在
> `docs/upstream-customizations.yaml`**；这里只留「来源与边界」的索引，不再重复列举。

### G-U 多账户分区（多配置档）

- 来源：`75abc28` 起的多账户改造 + 后续修（`e9536ac` 跨节点 deadline、`server/app/workspace_test.go` 契约）。
- 边界：**API 层不做鉴权是用户决策**（多账户=多配置档，不是安全边界）；按账户分区由
  `api.ScopedRouter` 按 `user=` 派发到每账户一份 `AppContext` 实现，**211 处 `h.AppContext` 一处都没改**。
  `StreamHub` 必须每账户独立；`MetaDir()` 分账户、`SharedMetaDir()` 不分（上传与批注按注册表记的
  `MetaLocation` 算、不看账户）。前端 `user=` 只在 `services/base.ts` 注入（三处唯一汇聚点）。

### G-V 控制面 / 数据面分离（control / worker 角色）

- 来源：`cli/cmd/startup_config_test.go`、`server/internal/nodeinfo/role.go`、`web/src/services/controlPlane.ts`。
- 边界：控制面（账户表/偏好/提示词库/看板模板/节点表/WebPush/relay/update）只有主节点一份真相；
  数据面（项目/会话库/任务库/文件/git/进程池/定时任务）每台一份。
  **修复必须落在 `cli/cmd`** —— `make build` 装的是 `./cli/cmd`，`server/cmd/mindfs-server` 只有
  `make dev-backend` 用，发布二进制里没有它的代码（第一版修复改错地方，单元测试绿但真机 role 仍是 control）。
  开启方式：`-role worker` 或 `<config-dir>/config.json` 的 `{"role":"worker"}`；**不传 `-config` 时读这个缺省路径**。

### G-W worktree 会话 repoint

- 来源：`0f4de0d`（repoint 主体）、`cfdc6a7`（源转录已在主 slug 时就地处理）、`6f4dd92`（文档补判据）。
- 边界：**先判断要不要 repoint** —— Claude Code 按 spawn cwd 决定 slug，会话只要有一次在主 checkout 起过，
  转录就已经在主 slug 下，此时 worktree 目录被删也**不会**聊不了，只需清 `related_worktree_json` 里的坏路径。
  转录真在 `--worktree-*` 目录下时才走完整 repoint，且**顺序不可换**：`pool.Close` → 搬文件 → 一个事务里换绑定 id
  并推进游标 → 清归属。

### G-X 置顶（pins）

- 来源：`server/internal/pins/`、`http_pins.go`、`web/src/services/pins.ts`（2026-10-05 起）。
- 边界：**权威在主节点、但按账户一份**（两个维度各管一件事）。会话键是 `rootID::sessionKey`，**不带 node id**；
  会话库的 `sessions.pinned_at` 已退役（只留列，不再读写），刻意不与置顶表做并集。
  **不做跨设备实时**（用户 2026-10-04 定），只在切换项目等导航动作时刷新一次。

### G-Y 看板模型重做（拒绝上游 task_groups 编排体系）

- 来源：CLAUDE.md 事实 13 + §0-B 的架构级取舍。**这是最容易被上游合并冲掉的一组。**
- 边界：本地为「卡=任务、列=全局状态、无并发调度」。任务自带流水快照（`Task.Stages` 创建时从预设拷贝，
  之后与预设无关；旧任务在 `loadForMove` 里惰性回填）。上游的 `kanban.Start` 调度器、
  `TaskGroupPanel`/`TaskCardText`、`patchTask`/`fetchTaskGroups`/`groupOperation`、
  `GET/PATCH/DELETE /api/tasks/{id}` 与 `/read/{resource}` 路由、`openTaskEditDialog` 全部丢弃。
  `SchedulerAdmitted` 保留为 DB 列兼容位（无调度器时恒 true）。
  **建任务必须内联流水**（模板库只在主节点，任务可建在运行节点上；`taskStagesForCreate` 每次带 `stages`）。

### G-Z 跨项目工作台

- 来源：`web/src/components/workspace/*`、`useWorkspaceBoard.ts`、`GET /api/tasks/overview`。
- 边界：无项目时渲染跨项目工作台；卡片操作按卡片自带 `root_id` 派发；详情先切项目再选中任务。

### G-AA 前端 App.tsx 模块化重构

- 来源：`458495b`（WS 事件处理器整块搬出）、`e7a31d2`（流写缓存抽出）等一连串 refactor。
- 边界：`App.tsx` 原为 ~15k 行巨文件，拆成 `web/src/app/`（19 模块）+ `web/src/renderer/`。
  §0-B-H02/H13 的两大段（上游 module-level helper 与巨型 effect）取 local。

### G-AB 会话投影与审计

- 来源：`usecase/session_projection.go`、`session/audit.go`。
- 边界：把「会话对外投影」与「交换审计」各收敛成一处；上游没有这两层。

### G-AC ACP 运行时与 agents.json 用户层合并

- 来源：`326c380`（写用户层只读用户层）、`server/internal/agent/acp/*`。
- 边界：ACP 进程必须显式回收；写用户层 `agents.json` 时**只读用户层再合并**，
  不能把合并后的全量写回用户层（否则上游新增 agent 再也不出现）。dsh/ACP 起不来的三个原因见记忆与 `docs`。

### G-AD 会话列表 / 树 / 归档 / 搜索

- 来源：`sessionListMerge` / `sessionTree` / `sessionGroupDisplay` / `ArchivedSessionsPanel`。
- 边界：合并、建树、分组显示都是纯函数（便于契约测试）；上游没有这层。

### G-AE UI 基础设施（自建对话框 / Toast / 面板外壳）

- 来源：`web/src/services/dialog.ts` + `DialogHost`；`no-system-dialogs.test.mjs` 守着。
- 边界：**禁止组件里直接用 `window.prompt/alert/confirm`**（上游带进来过，见 §0-B）。
  弹窗外壳用 `PanelShell`（带未保存改动确认）。

### G-AG 输入区与编辑器（ActionBar 拆分 / CodeMirror token 编辑器）

- 来源：`web/src/components/action/*`、`web/src/components/editor/*`。
- 边界：§0-B-H16 的 533 行上游输入区实现取 local；@文件/@会话 引用走 token 编辑器。

### G-AH 文件树 / Git 视图 / 文档预览

- 来源：`web/src/components/Root{Git,Related,Worktree}ContentView.tsx`、`services/{git,documentPreview}.ts`。
- 边界：文件与 Git 请求必须带 `nodeId`；根视图拆三个页签。

### G-AI PWA / 陈旧资源自愈 / 子路径 ServiceWorker

- 来源：`73db73e`（不再整目录删除 web 哈希资源，让过期标签页自愈）、`6eb9457`（G-M 并入）。
- 边界：换哈希后旧标签页必须能自愈；SW scope 要剥 `/mindfs`。

### G-AJ 偏好 / 定时任务 / 提示词 / 更新 / WebPush

- 来源：`server/internal/{preferences,scheduled,update,webpush}`、`usecase/prompts.go`。
- 边界：共享范围表见 CLAUDE.md 事实 12 —— 偏好/节点表/WebPush 订阅/提示词/看板模板**共享**；
  看板任务库与定时任务**按账户**（它们按本账户项目调度，且执行时用本账户的 session manager，共享实例说不清该跑谁的会话）。

### G-AK 本地化（中英双语键）

- 来源：`web/src/i18n/locales/{zh-CN,en-US}.ts`。
- 边界：本地键必须与上游键并存；合上游时 i18n 两侧是最容易被整文件覆盖的文件。

### G-AL 构建 / 部署 / 收尾流程脚本

- 来源：`scripts/deploy-all.sh`、`scripts/{build-all,install,migrate-control-plane-to-primary}.sh`、
  `Makefile`、`.claude/skills/{ship,upstream-merge}`。
- 边界：`ship` 一条命令完成门禁→提交推送→两侧编译安装→WSL 重建重启→两端对账。
  `73db73e` 修的是「不再整目录删除 web 哈希资源」。合上游后 `Makefile` 的本地 target 要保住。

### G-AM 插件注册表与视图目录

- 来源：`web/src/renderer/{viewCatalog,registry,Renderer}`、`plugins/manager.ts`。
- 边界：视图目录决定可用视图与页签顺序，「主视图记忆」依赖它。

---

## 4. 未提交工作区（2026-10-06 清空）

> 本节原先逐 hunk 记录 main 工作区里**未提交**的改动。那些改动已全部提交、工作区已干净，
> 继续留档会谎报「还有东西没落盘」，故清空。
>
> 沿革结论（当时的归类，供 `Scope:` 反查）：`App.tsx` 的节点/会话聚合 hunk → G-J；
> `SessionList.tsx` 的 `groupIsCurrentNode` 空串互等 → G-S，`SubSessionIcon color-mix` → G-L；
> `BottomSheet.tsx` 的 `rAF`+/70%/+双击全屏 → G-O；`SessionViewer.tsx` 流式与工具卡 → G-G；
> `stream/ToolCallCard.tsx` 图标主题化 → G-L；`layout/AppShell.tsx` 侧栏拖拽 → G-L。
>
> **当前的文件级真相在 `docs/upstream-customizations.yaml`**，本节不再是核对依据。

---

## 5. 评估

### 总体

- **合理且必要**：G-B/C/E/F/G 的正确性与性能、G-N/O 的交互、G-P/Q 的“ID 不变/别名可变”语义、G-K 的前缀/代理基础设施，收益与改面匹配。
- **需收敛**：视觉/主题（G-L）与多节点/前缀（G-J/K）在同窗并行，`App.tsx` 多次大范围重排，回滚与回归成本高。
- **破坏性权衡**：G-I（`*` CORS + e2ee 硬删）为产品级取舍，公网/多租户下应开关化。

### 分组点评与更优路径

**G-A** 合理；无过度设计。

**G-B** 正确。用 `errors.Is(ErrNotExist)` 分流最小；更优：抽 `mapFSError(err)` 统一 400/404。

**G-C** 合理。`raw 404 60s` 缓存避免重试风暴；更优：与 G-E 的后端节流共用同一 TTL 配置。

**G-D** 合理。`useRefreshSpin(450ms)` 复用；无过度。

**G-E** 合理。游标增量+`IN` 单查+WAL 为标准；`SetMaxOpenConns(4)` 适中。更优：补充一次 `pprof` 基线，`2e85356/7837c28` 压缩为两批。

**G-F** 合理但需克制。双重 30ms debounce + `useDeferredValue` + IDB 截断有效；更优：`cacheVersion` 与 `streamVersion` 合并为单一 `useDebouncedVersion(30ms)`。

**G-G** 精准。`ws.go` 单读 goroutine 异步派发 + `Interrupt 3s` 命中热坑；更优：抽 `CancelWithTimeout(ctx,3s)` 辅助。

**G-H** 自洽但一次性删除 4 文件 + 多处入口，后续恢复成本高。更优：无——已删即删，评估同 G-I 一并开关化更佳。

**G-I** 过度一刀切。`*` CORS 与 e2ee 全删短期清爽、长期难回退。更优：`config.yaml: auth/e2ee/cors.allowlist` 开关，默认关但保留能力。

**G-J** 正确但分散。`rootNode/appURL(nodeId)` 透传完整；更优：`getRootNodeId` 单一推导，`scope.ts` 即唯一 `nodeId:projectId` 源，避免 `nodeRegistry` 另起一套。

**G-K** 最值得保留，但重复最多。`internal/deploy/prefix`/`api/prefix`/`prefix.ts`/`nodeBase.ts` 四处各写前缀规范化，`vite.config/index.html/runtime` 再各写基路径。更优：后端仅 `internal/deploy/prefix`，前端仅 `prefix.ts` 为入口，其它导入。

**G-L** 方向对、批量大。`7a12971` 单提交 28 文件含路由与视觉，回滚难。更优：拆 `useNodeTheme` + CSS 变量，分 2–3 批，加快照测试。

**G-M** 合理。原生 `scopeRelativePathname` 替代 nginx patch 干净；无过度。

**G-N/O** 合理。均有契约测试；`BottomSheet` 的 `rAF` 与 `pointercancel` 细节完整。更优：`App.tsx` 锁定状态机抽 `useSessionLock`。

**G-P/Q** 细致。`display_name` 不漂 ID、`fork→独立` 与回流、导入标题 20 字截断均到位。更优：`manager.go`(2815行) 按 `alias/external/fork` 拆文件。

**G-R** 必要但与上游 `869de8a` 同改 `probe/session`，二选一时以本实现的分层 `os.Environ→agents.json→settings` 为准更彻底。

**G-S** 正确收敛。`scope.ts` 与 `groupIsCurrentNode` 的空串互等修复是关键细节；更优：`SessionList` 的 `expandedProjects[key] ?? groupIsCurrentNode` 与 `scope.ts` 同源，避免两套判等。

### 重复/冲突/错误

- **重复**：前缀/URL 规范化四处各写；`scope key` 与主题色 `nodeColor` 推导两套。
- **冲突**：`af0a37f vs 869de8a`（模型探测）、`ab8999f/040fbd2 vs d6cc497/0338765`（文本等价）。
- **过度**：G-L 单批过大、G-I 硬删、`manager.go` 单文件膨胀。
- **未见阻塞错误**：抽样 `manager` 游标/WAL、`ws` 异步、`scope` 隔离均无明显逻辑错；测试随 `web/tests/*.test.mjs` 与 `*_test.go` 同步增加。

---

## 6. 后续收敛（按优先级）

1. **前缀单一源**：后 `internal/deploy/prefix` + 前 `prefix.ts`，其余改为调用。
2. **scope 单一源**：`scope.ts` 为前端唯一 `nodeId:projectId` 推导，主题/折叠/loadMore 复用。
3. **e2ee/CORS 开关化**：`config.yaml: auth/e2ee/cors.allowlist`，默认关但可恢复。
4. **拆 `manager.go/App.tsx`**：`manager_alias/external/fork.go`；`useSessionLock/useNodeTheme` 等 hooks。
5. **合上游 `v0.4.8~v0.4.9`**：以 `af0a37f` 为准收敛 deepseek，以本地前缀/节点为准保留 G-K，弃上游等价文本。

---

## 7. 附录

- 生成：`git log --reverse 18b10ca..HEAD` + `git show --numstat` + 逐 hunk 核验；`git cherry -v` 判等价。
- 维护（**当前生效的约定**）：
  - **文件/测试/锚点的唯一真相是 `docs/upstream-customizations.yaml`**，不在本文件里重复列举。
  - 新增定制 → 先在 yaml 里登记（组 id + 症状 + 理由 + 文件 + 针对性测试 + 锚点），再跑 `make check-upstream` 确认全绿。
  - 提交信息标 `Scope: G-X`，与 yaml 的组号对应。**注意实际历史里这条只被遵守过一次**
    （`Scope: fixup` / `merge` / `chore` 等自由值居多）；本约定自 2026-10-06 起由 `check-upstream` 强制。
  - 合上游 tag 后：更新 yaml 的 `baseline`，重跑门禁 —— 红了就是那一组被覆盖了，按 yaml 里的「为什么必须保留」逐条恢复，
    **不要按上游实现重写**。
  - 新增分组的唯一前提：它有**独立的互斥边界**（一组 = 一块合上游时要么全留要么全弃的改动面）。

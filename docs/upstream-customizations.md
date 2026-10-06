# MindFS 上游定制清单与评估（改动级互斥）

> **当前基准：`9ab9572`（tag `v0.5.5`）= `git merge-base HEAD upstream/main`。**
> 核对口径：`git diff 9ab9572..HEAD --name-status`。**当前文件数不写在这里** ——
> 它每次提交都变，硬编码就会立刻过期（这行曾经写 380，实际 384）。
> 要当前值就跑 `make check-upstream`，它最后一行会打印「386 个 delta 文件被 36 组完整覆盖」。
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
  `SchedulerAdmitted` 字段与 `scheduler_admitted` 列于 2026-10-06 一并退役（不再读写；schema 行留在原地不做破坏性迁移）。
  原注释写它「无调度器时恒 true」是错的 —— 它从来没被赋成 true，只是没人读所以看不出来。
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

## 1. 总览（37 组）

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
| G-Q | 会话别名/外部导入/Fork 扁平 | 数据模型 | `e459a13:SessionList` 切片、`f746124/4c59fcf/ab8cfed` + 未提交 `manager` 徽标切片 | `fork→独立`/`agent+session_id→alias`/建会话即写 agent 绑定（空 `agent_session_id` = 尚无转录） |
| G-R | 模型识别与流式透传 | 修复 | `af0a37f` 主体、`ab8cfed:probe` 1M 切片、`e92fecf:AgentSelector` 切片 | DeepSeek/Claude 1M 探测与 `stream_hub/ws` 透传 |
| G-S | 作用域隔离与列表折叠 | 修复 | `3d3417a/ed44573/0591238` + `7a12971:App` 聚合切片 | `scope.ts`、`expanded/loadMore` 按 `nodeId:projectId` |
| G-T | 流式重放与正文正确性 | 修复 | 见 §3「G-T」—— `session.ts`/`useSessionStreamCache.ts`/`useSessionStream.ts`/`useRealtimeEvents.ts`/`App.tsx`/`stream_hub.go`/`ws.go`/`ws_test.go` | **挂载会话 = 快照重建（无游标续传）**、seq=0 只在在途时存在且**不编造 seq**、重放整批一条消息、`session.stream` 只有一种帧形状 |
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
| G-AH | 文件树 / Git 视图 / 文档预览（含关联文件统计批量接口） | 功能 | 见 §3.1 | 请求带 `nodeId`；根视图三页签；关联文件统计走批量端点 |
| G-AI | PWA / 陈旧资源自愈 / 子路径 SW | 修复 | 见 §3.1 | 原 G-M 并入 |
| G-AJ | 偏好 / 定时任务 / 提示词 / 更新 / WebPush | 功能 | 见 §3.1 | 共享 vs 按账户的边界见事实 12 |
| G-AK | 本地化（中英双语键） | 功能 | 见 §3.1 | 合上游最易被整文件覆盖 |
| G-AL | 构建 / 部署 / 收尾流程脚本 | 部署 | 见 §3.1 | `ship` / `upstream-merge` skill |
| G-AM | 插件注册表与视图目录 | 功能 | 见 §3.1 | 主视图记忆依赖它 |
| G-AN | 列表投影瘦身与条件请求（ETag / 304） | 性能 | 见 §3.1 | 列表响应省略恒空键 + ETag/304；客户端先判 304 再判 `ok` |

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
| `2e85356` | `server/internal/session/manager.go` | `exchanges/aux` JSONL 游标增量、列表 `meta-only` + `agent bindings IN` 单查、`updated_at` 索引（`pinned_at` 索引随该列退役，见 G-X） |
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
| 建会话即写 agent 绑定（`EnsureAgentBinding`） | `server/internal/session/manager.go:Create/EnsureAgentBinding/ensureAgentBindingUnsafe` + `server/internal/api/usecase/session.go:SendMessage` | **可见症状**：状态圆圈右下角的 agent 徽标（会话列表、看板任务卡片）在首轮跑完前只显示占位「AI」。原因：列表行是 meta-only 的（G-E），agent 只能由 `InferAgentFromSession` 的「`AgentCtxSeq` 恰好单键」兜底推出，而它靠 `session_agent_bindings` 回填 —— 绑定原先**只在回合结束**时写。**为什么必须保留的这一条**：与 G-Q 的空 id 守卫是同一块改动面 —— 占位行 `agent_session_id=''`，靠 `upsertExternalSessionNameUnsafe`（空 id 直接 return）和 `lookupSessionAliasForAgentUnsafe`（空 id → not-found）挡住，否则两个都没跑过的同 agent 会话会按空 id 串成同一个别名。合上游时两者要么一起留、要么一起弃。占位行的 `agent_ctx_seq` 必须是 0：非 0 会被 `prependSwitchHint` 当成「已同步到此行」而吞掉 agent 切换提示 |

**针对性测试（防覆盖，改动时同步维护）**

| 测试 | 钉住什么 |
|------|---------|
| `session/manager_test.go` → `TestManagerCreateBindsAgentForListInference` | `Create(Agent:"claude")` 之后**走列表路径**（`List`，从库里重读）断言 `InferAgentFromSession == "claude"` —— 就是徽标那个 bug 本身。注释掉 `Create` 里的写入即变红（已做变异验证） |
| 同上 → `TestManagerCreateBindsAgentWithoutTranscriptID` | 占位行 `AgentSessionID == ""` 且 `AgentCtxSeq == 0`；随后 `UpdateAgentState(...,"real-id")` **覆盖**同一行（行数仍为 1），列表仍报 claude |
| 同上 → `TestManagerEnsureAgentBindingIsIdempotent` | 回合开始的补写不得把已有真实 id / ctx_seq 打回空；换 agent 时老绑定不动、新 agent 立刻有占位行；空 agent 静默跳过 |
| 同上 → `TestManagerEnsureAgentBindingDoesNotLeakEmptyIDIntoAliases` | 空 `agent_session_id` 不得进 `session_external_names`（两个同 agent 的空 id 会话不能串名），`LookupAliasForAgent(agent, "")` 必须为 false |
| `api/session_created_broadcast_test.go` → `TestEnsureAgentSessionBroadcastsSessionCreated` | 看板建会话那条路径（`EnsureAgentSession`）确实把 `Stage.Agent` 交给了 `Create`，且 `session.created` 的列表行带 `agent:"claude"`。**注意**：这条**不**是徽标 bug 的锚点（广播用的是内存对象，`AgentCtxSeq` 在 Create 里就已填好，删掉绑定写入它照样绿）；它钉的是「路径得先把 agent 传进来」——把 `Agent: exec.Stage.Agent` 改成空即变红（已做变异验证）。真正的 bug 锚点是上面第一个 `manager_test.go` 那条 |

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

### G-T 流式重放与正文正确性（2026-10-06 重写）

**互斥边界**：`session.stream` 的**重放下发协议**与 `seq=0` 瞬时行的生命周期。
**合上游时若与上游冲突，以本组为准** —— 这里的每一条都对应一个已实测的可见缺陷。
**完整论证见 `docs/session-streaming-rework.md`（协议层）与本文件顶部的事故记录。**

**这一组在 2026-10-06 被整体反转了一次**，先写清反转了什么，否则合上游时会把旧版复原：

| 上游/旧版这么做 | 现在这么做 | 为什么 |
|---|---|---|
| `session.ready` 带 `event_cursor`，服务端**按序号续传** | 不带任何进度；服务端**整份重发**一帧 `session.stream{reset:true, events:[…]}` | 游标协议要求「客户端缓存 ↔ 服务端游标」严格同步，这条不变量在这套系统里**维持不住**：写游标的代码只认单条 `payload.event`，而重放走的是批处理帧 `payload.events`，真实抓包中 `event_cursor` **恒为空**。于是每次挂载事实上都是全量重发，客户端却按「增量」去 merge |
| 客户端用 `mergeStreamedText` 在拼接处猜「这片是不是重发」 | **删除**；拼接就是拼接 | 上游那条补丁修的是症状。协议改成快照重建后每片只投一次，判据本身不再需要 |
| 服务端 `completed` 表 + 补发 `done(replay:true)` | **删除** | 客户端对这条回执的两处分支本来就是空操作（不放提示音、不重锚定）。而它引发的自持环是真的：`done → restoreActiveSession → session.ready → ReplayPending → done`，实测 18 次/秒。真实结束的 done 用 `liveOnly=false` 广播，重放中的客户端**本来就在收件人里** |
| `buildBaseTimeline` 用 `inferredSeq` 给瞬时行按位置补 seq | **删除**，无 seq 就是 0 | 补出来的 seq 属于**别的真实持久行**，而 `exchange_aux` 按 seq 索引 —— 瞬时行于是认领了别人的工具卡，用户看到「正文下方多出一整套工具卡」（`data-session-seq="19"` 那条）。它同时骗过了 `seq > 0` 这个「已持久化」判据，让没落盘的行显示 fork 按钮 |

**当前契约（合上游时的核对清单）**

| 改动 | 文件 | 为什么必须保留 |
|------|------|----------------|
| 挂载会话 = **快照重建**：服务端恒回一帧 `reset:true` 的整批事件 | `server/internal/api/stream_hub.go`（`ReplayPending` / `buildSessionStreamBatchResponse`） | 让「客户端的瞬时尾巴 = 服务端 buffer 的纯函数」无条件成立（`first` 标志保证**空 buffer 也发**）。重放 N 次结果逐字节相同，重复渲染那一整类 bug 在结构上消失 |
| `reset` 是 `buildSessionStreamBatchResponse` 的**参数**，不是常量 | 同上 | 首帧（快照，`reset=true`）与排空循环的**续投**帧（`replayStepToClient`，`reset=false`）**形状相同、语义相反**。写成常量会让「排空期间新到事件」把刚重建好的整条尾巴清掉 —— 越活跃的会话越容易命中。测试 `TestReplayDrainStepIsNotAReset` 钉住 |
| 重放**整批一条消息**下发 | 同上（`replayStepToClient` / `buildSessionStreamBatchResponse`） | 原实现逐条 `SendToClient` —— 几百条事件 = 几百条 WS 消息 = 几百次渲染 = 「首次打开运行中会话时逐渐刷出来」 |
| 客户端 `session.stream` 只留**一种**帧形状 | `web/src/services/session.ts`（`emitDecrypted` 把 `reset` 批帧拆成 `session.stream.reset` + 逐条单事件） | 曾经批处理帧只有全局监听路径认，按会话分发路径拿到的是 `undefined` → `onStream(undefined)` TypeError（实测每个批帧一次）。归一到单条形状后两条路径共用 |
| `reset` 帧只清瞬时尾巴、空 `events` **不调** `handleSessionStream` | `web/src/app/useRealtimeEvents.ts` | 空 `events` 被当成内容会造出一行空 assistant |
| `isTransientExchange(ex)` = 「`seq` 为空**且不是用户行**」 | `web/src/services/session.ts` | 用户行例外是刻意的：客户端发出消息时插一条 seq=0 的**乐观回声**，服务端事件流里从来没有它 —— 照判据清尾巴会让刚发出去的消息当场消失。它是全前端唯一的瞬时行判据 |
| `dropTransientExchanges` 挂在 `reset` / `compact` 两处，`done` 上**只在不会重锚定时**才清 | `web/src/services/session.ts` + `web/src/app/useRealtimeEvents.ts` | 补齐**从来不存在**的清理出口。此前 seq=0 无任何清理机制，实测跨 7 次 compact 堆积到 1100+ 条。**`done` 那处是有条件的**（2026-10-06 改）：会重锚定的会话把尾巴交给窗口组装退役（见下一条），提前清 + 重锚定被拦下 = 这一轮永远不见 |
| **回合收尾只有一个出口**，且**先装窗口、再退尾巴** | `web/src/app/useRealtimeEvents.ts`（`handleSessionStreamDone` → 返回「本轮是否真的结束」；`willReanchor ? base : dropTransientExchanges(base)`；重锚定调用在本函数末尾） | 曾经完成路径上并存两套判断、顺序还相反：处理器先无条件清尾巴，再用 `isSessionStreaming` 猜「要不要重锚定」。2026-10-06 真机复现：**正在完成的正文瞬间消失，只剩已落盘的用户行**（时间线 137 项 → 2 项），切走再回来才由 viewer 的 init 补回。清尾巴是同步的、装窗口是异步的 ⇒ 非原子交接。留下尾巴是安全兜底：下次成功加载会退役它，短暂重复远好于凭空消失 |
| `emitDecrypted` 里 `updateActiveStreamState` 必须在 `emit` **之前** | `web/src/services/session.ts` | `emit` 同步调用监听者。顺序反过来，监听者在 `session.done` 里读到的流状态是**上一帧**的（`activeStreams` 尚未删除该键，恒为「仍在流」）。它同时是上面那条的成因，也是 `useSessionStream.ts` 里那个真实消费者的正确性前提 |
| `composeLoadedExchanges(server, cached, inFlight)` | `web/src/services/session.ts` + `App.tsx` 两处调用 | 加载时的**唯一**组装规则。瞬时行（seq=0）只在**在途**时保留 —— 服务端从不经窗口回包下发它们，所以绝不能去服务端回包找（曾经三处那么写、三处恒空，导致切回运行中会话时在途内容整块丢失）。另：只保留**最后一次 compact 之后**的瞬时行 |
| `persistedSeq(ex)`：没有真 seq 就返回 0 | `web/src/hooks/useSessionStream.ts` | 见上表最后一行。**不得**再按位置推断 |
| 重放批次必须在 `emitDecrypted` 归一形状 + 只在全局监听路径保留 reset 分发 | `web/src/app/useRealtimeEvents.ts` | 两条分发路径只留一种帧形状，是上面那条 TypeError 的修复本身 |
| **overlay 判定必须在纯模块 `components/sessionOverlay.ts` 里**，组件只做接线 (`SessionViewer` 调 `computeTailOverlay`) | `web/src/components/sessionOverlay.ts` + `SessionViewer.tsx` | 它原来住在组件的 `useMemo` 里 ⇒ **测不了** ⇒ 测试只能写「同构副本」（`session-window.test.mjs:780`）或对源码做字符串手术再 eval（`session-window-overlay-dedup.test.mjs`）。**副本会漂移**：冲突②③④⑭ 这四条真实缺陷被副本挡了几个月没人看见。搬出来后副本已删、换成对真实现的薄适配器；纯模块无 import，Node 可直接 import |

| **`truncated` 标记必须诚实**（发生过淘汰就标 true） | `web/src/services/session.ts` | 曾由 `forceNoTruncate` 在窗口态写成 false，注释说是「跳过 truncated 全量回补」—— 那是**读侧**的事（`baseTruncated = windowed ? false : …`）。写侧说谎的后果：窗口态标记是内存的、刷新即丢，而标着「完整」的 IDB 记录还在盘上 ⇒ 下次全量路径只做增量 ⇒ **被砍掉的历史永远不补** |
| **compact 不是服务端历史的重置点**（据此的清理与切分规则都已删除） | `session.ts` `composeLoadedExchanges` · `useRealtimeEvents.ts` compact 分支 | 交换 JSONL 是 **append-only**（唯一 `os.Remove` 在两处 `DeleteSession`），compact 只写一条 `CompactNotice` aux。原规则按它切/清瞬时行，而 compact 是**轮内**事件 ⇒ 丢掉本轮前半段（服务端没有替代品） |
| **瞬时行的判定只能有一处实现**（`isTransientExchange` / `isPersistedSeq`） | `session.ts`（导出）+ `SessionViewer.tsx` / `App.tsx` 的调用点 | 曾经两把尺：谓词保留 seq=0 用户行、`mergeSessionExchanges` 内联 `seq>0` 丢弃它 ⇒ 同一条行的去留取决于先跑哪条路径。判据再内联一次，bug 就悄悄回来 |
| **overlay 判定在纯模块 `components/sessionOverlay.ts`**，且持久行的规则只有「窗口是否真的含这一行」一条 | 同上 + `SessionViewer.tsx` 接线 | 原判定住在组件 `useMemo` 里 ⇒ 测不了 ⇒ 测试只能写同构副本/字符串手术，副本挡住了冲突②③④⑭。另：A2 分支「`seq<=latestSeq` 就跳过」假定窗口一定含它，而搜索跳转会用中段窗口整体替换 ⇒ **渲染空洞** |
| **aux 拼接必须幂等**（`appendExchangeAuxDelta` 去重） | `session.ts` | 增量同步的重投递是常态（游标漂移/同步重跑/重锚定重复投递），拼接语义下同一张工具卡会被加两遍 |
| **`PENDING_STREAM_LIMIT`**（无人订阅时的事件缓存必须有上限） | `session.ts` | 长回合 + 用户始终不打开该会话 = 内存无界增长 |
| **WS 帧必须都有消费者**（`answer_question.accepted` 已删、`session.repointed` 已接） | `server/internal/api/ws.go` · `web/src/app/useRealtimeEvents.ts` | 发出但没人接的帧是自产自销；`repointed` 尤其可惜 —— 服务端注释写明「广播一次强制刷新」，前端从未消费 |
| **附件的文本 token 只有一个组装点**（`formatFileToken` / `fileTokenPath`） | `services/upload.ts` + 4 个调用点 | 同一形状在 App / TaskDetailPanel / ActionBar / tokenEditorUtils 各写一遍，改一处必漏三处 |
| **`/plan` 前缀的检测/剥离/拼装同源**（`inputTransforms.ts` 独占字面量） | `inputTransforms.ts` + App.tsx | 同一前缀被 5 处各自解析/拼装 |
| **清除 pending 必须覆盖五处存储**（走 `clearLocalPendingForSession`） | `App.tsx` | 实测两处只清 2/5（发送取消、发送失败）⇒ 缓存与抽屉永久停在 pending（「灯不灭」） |
| **乐观回声的两条判据各只有一处实现**（`settlePendingAcks` / `isSamePendingEcho`） | `session.ts` + `App.tsx` / `useRealtimeEvents.ts` | 原形状散在 7 处，含两份完全相同的局部 `clearPendingAck` |

**针对性测试（防覆盖，改动时同步维护）**

| 测试 | 钉住什么 |
|------|---------|
| `web/tests/session-timeline-seq.test.mjs` | **不得出现 `inferredSeq`**；`persistedSeq` 对缺失/0/负数/NaN/垃圾一律返回 **0**（不是「上一条 + 1」）；seq=0 时必须取**空** aux（不许认领真持久行的工具卡）；`seq > 0` 仍把守 fork 按钮与 `data-session-seq` |
| `web/tests/session-window.test.mjs` → `dropTransientExchanges` / `isTransientExchange` | 清 seq=0 的非用户行；**seq=0 的 user 行必须活下来**；缺 seq / 非数字 seq 归为瞬时；无瞬时行时返回**同一引用** |
| 同上 → `composeLoadedExchanges` 8 组 | 冷缓存 / 在途保留全部瞬时行 / **不在途全丢** / compact 边界切分 / 无 compact 不砍 / 同对象去重 / 缓存更全 / 空输入 |
| 同上 → `mergeStreamedText` **已删除** | 反向断言 `mergeStreamedText === undefined` —— 它再出现就说明协议退化回了增量续流 |
| `web/tests/upstream-restore.test.mjs` | d36cc53 的目的（重连不丢在途内容）改由**快照重建**达成：断言服务端 reset 帧存在、客户端只有一种帧形状，并**反向断言** `eventCursors` / `getEventCursor` / `clearEventCursor` / `event_cursor:` / `mergeStreamedText` 均不得回潮 |
| `web/tests/session-done-replay-no-reanchor.test.mjs` | 反向断言 `payload?.replay` 已不存在；**回合收尾只有一个出口**：`handleSessionStreamDone` 内含重锚定调用、且尾巴清退是条件式（`willReanchor ? base : dropTransientExchanges(base)`）、其体内不得出现 `isSessionStreaming`；done 处理器体内不得出现 `isSessionStreaming` / `reloadSessionForReplay`；`emitDecrypted` 必须先 `updateActiveStreamState` 再 `emit` |
| `server/internal/api/ws_test.go` → `TestReplayBatchIsOneMessage` | 重放必须是**一条**带 `events` 数组的消息，且不同时携带单条 `event`；首帧必须带 `reset:true` |
| `server/internal/api/ws_test.go` → `TestReplayDrainStepIsNotAReset` | **续投帧不得带 `reset`**（会清掉快照刚重建的尾巴），但形状必须与快照帧一致；空 `events` 归一成 `[]` 而非 `nil` |
| `server/internal/api/ws_test.go` → `TestDoneCarriesNoReplayReceipt` | `session.done` 载荷只允许 `root_id`/`session_key` 两个键 —— 任何补发的回执标记（`replay`）都必须再加一个键，加不出来就说明环的燃料回来了 |
| `server/internal/api/ws_test.go` → `TestReplayAfterClearYieldsEmptySnapshot` | 回合清空后重新挂载拿到的是**空快照**（`collectReplayStep` 无事件且 `live`），不是上一轮的尾巴 || `web/tests/session-overlay-unit.test.mjs` | overlay 的**行为**：`computeTailOverlay` 的 7 个分支。含 4 条【红】②③④⑭（A2 渲染空洞 / aux 与窗口脱节时卡片两边都不渲染 / 短回复落盘后显示两遍 / 只差空白的短用户消息显示两遍） |
| `web/tests/e2e-message-behavior.test.mjs` + `tests/e2e/harness.mjs` + `scripts/mindfs-iso.sh` | **隔离实例 E2E**（`MINDFS_E2E=1` 才跑）：冷启动窗口 / **完成瞬间交接**（done 后必须发起窗口重锚定 + 项数不下降）/ 应用内切走切回 / 查库判据。不设环境变量时整文件跳过，普通 `npm test` 不受影响 |


**验证记录**：

- **已实测**：控制台 `onStream` TypeError **3 次 → 0 次**（3 = 探测窗口内收到的批帧数，1:1 对应）。
  这一条**只装前端**（静态资源按请求读盘，无需重启）就成立 —— 它归「形状归一」，与服务端无关：
  旧服务端的批帧不带 `reset`，也会被正确展开。
- **待重启验证**：CDP 探针在「切走 → 切回」后尾部 DOM 节点数必须 **137 → 137**
  （修复前 137 → 138，多出的即那块被认领了 aux 的正文）。
  只装前端时仍是 **137 → 138** —— 因为客户端没有 `reset` 就不知道要清尾巴。
  服务端重启属用户操作（`sudo systemctl restart mindfs` 会杀掉托管的 claude 子进程），
  完整对照表见 `docs/session-streaming-rework.md` §4.1。
- **已实测（2026-10-06，headless CDP 真机复现）**：会话 `1791236887-d02bc42e1eea`，
  两次连跑互为对照 ——
  - 第 1 跑（真实回合未发出，只有 1 条 done）：`session.done` 1 条、**done 之后本会话请求数 0**、
    时间线 **137 项/11970 字 → 2 项/1614 字**（只剩两条已落盘的用户行）。**这就是用户报的症状**。
  - 第 2 跑（真实回合发出）：`session.done` **80 条**、done 后本会话请求 **78 次**、
    `done : ?latest=20` 严格 1:1:1、≈8 次/秒自持环；时间线 84 项保持。
  - 两跑同会话、同 replay 目标集合，唯一差别是时序 ⇒ 证明那道门在 lagging 状态下恒为假。
    第 2 跑的环由**旧后端**的 `completed` 表补发 done 驱动（该表已删），
    环一旦起来就把窗口一直重装，**症状被掩盖成「正常」** —— 这正是它看起来时隐时现的原因。
- **待重启验证**：本机跑着的进程是 05:01:50 启动的**旧二进制**（前端 dist 是 12:39 的），
  混合态。新前端下环照样会起（因为旧后端仍在补发 done），要验证「环消失 + 完成不再塌」
  必须先 `sudo systemctl restart mindfs`（**只能由用户执行**，会杀掉所有托管的 claude 子进程）。
  复测脚本 `/tmp/probe-done.mjs`，期望：done 帧数 1、done 后本会话请求数 ≥1、时间线项数不下降。
- 会话 `mindfs/性能优化 #30` 冷启动到等待 ask 的整轮正文完整（**这是「重放不能删」的活证据**：
  整个回合只存在于服务端内存的 `ReplyingList` 里，持久化 JSONL 只有两条 user 行）。
- `go build` / `go test ./...` / `tsc --noEmit` / `node --test tests/*.test.mjs` 全绿；
  关键判据均做变异验证（改回旧写法即红）。

---

### 3.1 补全分组（2026-10-06 新增，19 组）

> §3 的 G-A…G-S 是 v0.4.7 时代按 hunk 归的类，只覆盖了当时 68 个文件。
> 2026-10-06 用 `git diff $(git merge-base HEAD upstream/main)..HEAD` 对账，定制面实为 **380 文件**，
> 补出以下 19 组。**每组的文件、测试、锚点、可见症状、为什么必须保留都在
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
  `SchedulerAdmitted` 字段与 `scheduler_admitted` 列于 2026-10-06 一并退役（不再读写，schema 行保留）。
  旧 `queued` 状态同理：`migrate()` 无条件折进 `pending`，前端已按「不可能出现」删掉该分支，
  由 `task_store_queued_migration_test.go` 钉住这条迁移。
  **建任务必须内联流水**（模板库只在主节点，任务可建在运行节点上；`taskStagesForCreate` 每次带 `stages`）。

### G-Z 跨项目工作台

- 来源：`web/src/components/workspace/*`、`useWorkspaceBoard.ts`、`GET /api/tasks/overview`。
- 边界：无项目时渲染跨项目工作台；卡片操作按卡片自带 `root_id` 派发；详情先切项目再选中任务。
- **卡片「跳会话」的 root 归属（2026-10-06）**：可见症状是**第二次**点同一张卡片的会话图标，
  会跳到「当前项目」下的同名空白会话（请求 `?root=<当前项目>` → 404），首点却是对的；
  换个顺序点、或先切回项目再点，同样中招。
  必须保留的理由：会话回包不带 `root_id`（root 只是请求参数），所以「这次跳转属于哪个项目」
  只能由前端自己钉住 —— 写缓存时补请求根（`restoreActiveSession` 的窗口/全量两分支 +
  `handleSelectSession` 的 `applySession`），读端用 `web/src/app/sessionJump.ts` 的
  `buildSessionJumpTarget` 以卡片 root 覆盖。上游没有这一层，退回上游实现症状立刻复现。

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
- **关联文件统计走批量接口（2026-10-06）**：`POST /api/git/related-files/stats`。
  可见症状（丢了会怎样）：会话与看板里的 `+N −M` 徽标全空（前端只认这个端点），
  或流量与卡顿回来 —— 实测 80 个关联文件时，逐文件取完整 diff 会打出 3706 次请求/小时、
  约 24 MB/小时，且每个文件都要跑 name-status + numstat + 完整 `git diff`，
  而响应 6473B 里 5073B 是调用方一个字节都不读的 `content`。
  必须保留的理由：这是一个**端点级的成本修正**，上游没有对应实现；光改前端调用返不回去。
  同一批改动里还合并了热路径上的重复请求：多项目会话列表重拉统一走既有的 300ms 去抖
  （`scheduleMultiProjectSessionReload`）、`git status` 按 `(root,node)` 复用 in-flight。
  批量与逐文件必须**逐字段等价**，由 `gitview_test.go` 对拍钉住；「批量真的更省」由同一个
  文件里的 git 调用次数对拍钉住（PATH 垫片计数），不是靠注释声称。
- **客户端 git 缓存加上限（2026-10-06）**：可见症状是长期开着标签页翻 git 历史时
  内存一路涨（`gitCommitDiffCache` 存的是**完整 diff 正文**），或 localStorage 写满配额后
  静默丢失 —— `writeStorageJSON` 的 `try/catch` 会把配额错误吞掉，表现成「缓存莫名没了」。
  三个 Map 走 `setBounded`（上限 200，删最旧的一条近似 LRU），localStorage 的
  commit diff / commit files 走 `capStorageByPrefix`（上限 32 条）。
  **in-flight map 刻意不设上限** —— 它们在 `.finally` 里就删了，加上限没有意义，
  反而会让并发去重失灵；`git-cache-bounds.test.mjs` 里专门有一条断言钉住这一点。

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
- **`index.tsx` 的 Intl 格式化器缓存（2026-10-06）**：可见症状是长会话里逐条渲染卡一下 ——
  `new Intl.DateTimeFormat` 的**构造**是重活，实测一次渲染 1000 条消息要 **235ms**，复用同一实例只要 14ms（16×）。
  `SessionViewer` 的逐条渲染逐条调 `formatTime`，不缓存等于每次渲染都重建一遍格式化器。
  必须保留的理由：合上游一旦整份覆盖 `index.tsx`，缓存会**静默**消失（测试全绿、肉眼也看不出）。
  钉住它的测试是 `intl-formatter-cache.test.mjs`（源码守卫：四个格式化器都必须走缓存、缓存有上限、键序无关）。

### G-AL 构建 / 部署 / 收尾流程脚本

- 来源：`scripts/deploy-all.sh`、`scripts/{build-all,install,migrate-control-plane-to-primary}.sh`、
  `Makefile`、`.claude/skills/{ship,upstream-merge}`。
- 边界：`ship` 一条命令完成门禁→提交推送→两侧编译安装→WSL 重建重启→两端对账。
  `73db73e` 修的是「不再整目录删除 web 哈希资源」。合上游后 `Makefile` 的本地 target 要保住。

### G-AM 插件注册表与视图目录

- 来源：`web/src/renderer/{viewCatalog,registry,Renderer}`、`plugins/manager.ts`。
- 边界：视图目录决定可用视图与页签顺序，「主视图记忆」依赖它。

### G-AN 列表投影瘦身与条件请求（ETag / 304）

- 来源：`server/internal/api/helpers.go`（`respondJSONList`）、`http_tasks.go`（`overview` 投影）、
  `web/src/services/api.ts`。
- 边界：**列表类**响应的投影与缓存协商。服务端省略「恒为空」的键（`toSessionItem` 对每个被省略的
  键都有防御性兜底，`related_worktree` 是唯一的 `=== null` 用法且其消费者对 `null`/`undefined`
  一视同仁）、按内容算 ETag 支持 `If-None-Match` 回 304；跨项目看板不返回卡片从不读取的
  `stages` / `prompt_template`（`task.stages.prompt_template` 占 14.4%）。
- **别踩**：客户端必须**先判 304 再判 `response.ok`** —— 304 的 `ok` 为 false，顺序写反会把命中
  缓存的响应当成失败。合上游时为「列表响应瘦身 + 条件请求」这一整块，要么全留要么全弃。

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
  - **`tests:` 只列运行器真会执行的用例**（门禁断言 3 校验它匹配 `web/package.json` 里 `node --test` 的 glob）。
    对着真实服务跑的手工探针（如 `cross-machine-account.behavior.mjs`）是 fork 新增文件，登记进 **`files:`** 而非 `tests:` ——
    列进 `tests:` 等于把永远不执行、也永远不会红的东西算作覆盖。这个坑踩过一次：门禁曾只验「文件存在」，放过了它。
  - 提交信息标 `Scope: G-X`，与 yaml 的组号对应。**注意实际历史里这条只被遵守过一次**
    （`Scope: fixup` / `merge` / `chore` 等自由值居多）。门禁**不检查提交信息**（那要读 git 历史，脆且慢），
    所以这条目前仍靠自觉 —— 它的价值在 `git log --grep` 反查，不在门禁。
  - **上游文件里的定制 hunk 加一行 `// CUSTOM(G-xx): 为什么必须保留`**（`.sh` 用 `#`，`.json` 加不了注释就只登记锚点）。
    只加在**已存在的上游文件**里的小块 hunk 上 —— 纯新增的 fork 文件整体就是定制，合上游时整份冲突，不需要标记。
    **加标记的位置就是上游同点改动会冲突的位置，这正是要的效果**：冲突好过静默丢失。
    标记是**抽样**，不是覆盖率 —— 见 §7.1。
  - 合上游 tag 后：更新 yaml 的 `baseline`，重跑门禁 —— 红了就是那一组被覆盖了，按 yaml 里的「为什么必须保留」逐条恢复，
    **不要按上游实现重写**。
  - 新增分组的唯一前提：它有**独立的互斥边界**（一组 = 一块合上游时要么全留要么全弃的改动面）。

### 7.1 锚点是抽样标记，不是覆盖率（别把它读成「已检查 9%」）

2026-10-06 把 `CUSTOM(` 全量数了一遍，三件事必须分清 —— 混起来会得出「覆盖了 90%」这种错结论：

| 层 | 是什么 | 现在有多少 | 由谁强制 |
|---|---|---|---|
| `files:`（yaml） | 「这段代码是我们的」 | 417 条，覆盖全部 **384** 个 delta 文件 | 门禁断言 1（每个 delta 文件必须被某组认领）+ 断言 2（登了却不在 delta 里 = 已被上游吸收） |
| `anchors:`（yaml） | 「这个符号必须还在」 | **44** 条，分布 22 组 | 门禁断言 4（子串必须在源文件里命中） |
| `CUSTOM(G-xx)`（源码内） | 「**上游同点**改这里会冲突」 | **14** 处，落在 **13/140** 个 M 文件 | 无（人读） |

- 第三行才是「锚点」的字面意思：它**只加在上游已存在的文件里的定制 hunk 上**。纯新增的 fork 文件（217 个 A）永远不带标记 ——
  合上游时它们整份冲突，标记不提供任何额外信息。所以 **`CUSTOM(` 的占比天然不可能高，也不该高**。
- 它是**抽样标记**：14 处覆盖不了 140 个 M 文件里的每一处定制 hunk，**没标记 ≠ 没定制**。
  按标记数量估计「定制覆盖面」是错的，那是 `files:` 的职责。
- 它唯一的作用是在 diff 冲突现场**就地**说明「这里为什么必须留」；机器核对由 `anchors:` 与断言 1/2 承担。
  `.css` / `.json` 等加不了注释的格式，只登记 `anchors:`，不指望 `CUSTOM(`。

### 7.2 门禁全绿保证什么、不保证什么

| 绿了 = 成立 | 绿了 ≠ 成立 |
|---|---|
| 每个 delta 文件都被某组认领（没有未登记的定制） | 认领得**对** —— 组 id 是人挑的，分错组照样绿 |
| 每个 `files:` 条目仍出现在 delta 里（定制没被上游静默吸收） | 组内**每处 hunk** 都被检查过 —— 一条 `files:` 只证明文件被认领，不证明每行 |
| 每个 `anchors:` 子串仍在源码里命中 | 符号的**行为**没变 —— 子串在，语义可能已被上游改成别的 |
| 每个 `tests:` 文件存在，且匹配运行器 glob（真会执行） | 测试**测得住** —— 断言可以写成恒真，门禁看不出来 |
| yaml 与本文的组 id 双向一致 | 症状/理由写得对 |

所以门禁是**防漏登记**的网，不是「定制已正确」的证明。最后一条尤其要命：断言恒真时它照样是绿的。
写完测试要**把修复改坏、确认它变红**（变异验证），这一步门禁替代不了 —— 2026-10-06 就是这么抓出
`archived-panel.test.mjs` 里两条空断言的：一条把源码整段切片，内层 div 的 `overflow` 被当成了外层的
（删掉外层的裁剪，测试照样绿）；一条拿全文只出现一次的 marker 去滚动容器窗口里搜，永远搜不到。


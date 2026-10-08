# MindFS 上游定制清单与评估（改动级互斥）

> **当前基准：`9ab9572`（tag `v0.5.5`）= `git merge-base HEAD upstream/main`。**
> 核对口径：`git diff 9ab9572..HEAD --name-status`。**当前文件数不写在这里** ——
> 它每次提交都变，硬编码就会立刻过期（这行曾经写 380，实际 384）。
> 要当前值就跑 `make check-upstream`，它最后一行会打印「N 个 delta 文件被 M 组完整覆盖」——
> 具体数字**故意不抄在这里**：它每次提交都会变，抄一次就必然过期一次（这行写过「386 个 / 36 组」，
> 隔一天就成了假信息，而假计数会让人以为门禁在骗人）。
> **机器真相在 `docs/upstream-customizations.yaml`** —— 分组 id、文件、针对性测试、锚点全部以它为准，本文不重复列举；
> 覆盖率门禁 `make check-upstream` 强制「每个 delta 文件都被某组覆盖」。两文件的唯一强耦合点是**组 id 双向一致**。
>
> 本文档 §1-§3 分组为 v0.4.7→v0.4.9 时代沉淀，机制仍适用；v0.5.1 / 7757ca8 合并记录见 §0，v0.5.5 见 §0-B，接回 main 见 §0-B-2。
>
> 历史口径（2026-08-26 首次成文时的记录，**仅作沿革参考，不再是核对基准**）: 基准 `upstream/main` tag `v0.4.7` `18b10ca`，
> 对比 `HEAD = 0591238`，`git log --reverse 18b10ca..HEAD` 44 提交，`git diff 18b10ca...HEAD` 110 文件 `+8333/-4249`。
> 分组原则: **按 hunk 归类**——同一提交、同一文件不同行可归不同组；每行改动仅属一组，组间互斥、全体完备。
> 判定: `git show --numstat/--stat` 逐提交核验 + 逐 hunk 归类，`git cherry -v` 校验上游等价。

### G-BD. 移动端恢复后的 pending 对账与服务端事件时间

- **可见症状**：手机切到后台导致 WebSocket 断开并丢失 `session.done` 后，回复灯可能一直亮到下次偶然刷新；页面恢复后回复时长会把恢复时刻当成服务端事件时刻，显示出虚假的长耗时。
- **根因与边界**：页面恢复必须主动触发 `/api/replying-sessions` 对账；流事件的时间由服务端写入，客户端 replay 优先使用该时间，旧事件才回退到接收时刻。多节点分组以分组自身 `_nodeId` 构造 pending key，避免同名项目跨节点串灯。
- **针对性测试**：
  - `web/tests/pending-recovery-on-resume.test.mjs` → `visibilitychange`/`pageshow` 恢复时触发 pending reconciliation。
  - `web/tests/cross-node-replying-state.test.mjs` → 同名项目使用实际节点作用域投影，失败节点保留旧状态。
  - `web/tests/session-duration.test.mjs` → replay 使用服务端事件时间，不用恢复页面接收时间。



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

## 1. 总览（45 组）

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
| G-AO | 自动重载观测 | 架构 | 见 §3.1 | 只记录不干预：`sessionStorage` 计数 + 堆峰值 + 本轮首错 |
| G-AP | worktree 收尾按钮重做（**机械优先** + 四分支分流 + 幂等 + 真实状态） | 修复 | 见 §3.1 | 收尾键一直可点；目录消失 = 已收尾；拆完目录整个删 + 附件先搬 |
| G-AQ | 任务卡的三把键：完成兜底 / 取消 / 删除 | 功能 | 见 §3.1 | 待审核必有出路；取消只改状态；删除只删卡片 |
| G-AR | 移动端侧栏切换按钮移入中间主栏共用 header | 修复 | 见 §3.1 | 按钮插在中间主栏 36px header 两侧（不额外占行），仅移动端；侧栏自身不显示 |
| G-AS | ACP 提问（dsh `ask_user_question` 走 elicitation） | 修复 | 见 §3.1 | 只对 dsh 广告 elicitation.form；题目 id+文本关联；答案编码成 `question_<i>` |
| G-AT | 会话打开性能（窗口去重 / 载荷压缩 / 工具卡分组） | 性能 | 见 §3.1 | 会话打开热路径：取窗去重 + 窗口轻压缩 + 窗口 8 + 工具卡分组 + related-files 去抖 |
| G-AU | 会话拉取风暴与渲染主线程阻塞 | 修复 | 见 §3.1 | 加载 effect 只依赖身份不依赖快照对象；失败留痕且 404 是终点；`sessionCacheRef` 有上限；渲染无 O(n²)、滚动有节流 |
| G-AX | relay 绑定轮询测试的两条同步竞态 | 修复 | 见 §3.1 | 状态落地晚于 channel 发送；`requests` 必须无缓冲 |
| G-AY | pending 纯派生（done 丢失时停止符号/「正在思考」卡住） | 修复 | 见 §3.1 | 原先「在不在回复」在前端有五份投影，只有列表蓝灯那份每 5s 对账；其余四份只有 WS `session.done` 一个出口，断连/重绑竞态丢了那条事件就**永久卡在 true**。2026-10-07 改成纯派生：唯一真相 `multiProjectPendingByKey`，会话对象上不再存 pending |
| G-AZ | 任务卡视觉件（待审核徽标/列框去除/浮动滚动条/工作台角标移除） | 视觉 | 见 §3.1 | 待审核列补状态文字（与工作台同路径）；列框去除+padding 归零卡片加宽 18px；FloatingScroll 浮动滚动条不占宽；工作台「需要你 N」角标移除 |
| G-BB | 看板列头灰底 + 间距对齐工作台（列头背景/列表 padding/外层 padding） | 视觉 | 见 §3.1 | 列头灰底区分栏目标签与卡片；列表 padding 归零卡片加宽 12px；外层 padding 6px 8px 对齐工作台容器 |

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
| 建会话即写 agent 绑定（`EnsureAgentBinding`） | `server/internal/session/manager.go:Create/EnsureAgentBinding/ensureAgentBindingUnsafe` + `server/internal/api/usecase/session.go:SendMessage` | **可见症状**：状态圆圈右下角的 agent 徽标（会话列表、看板任务卡片）在首轮跑完前只显示占位「AI」。原因：列表行是 meta-only 的（G-E），agent 只能由 `InferAgentFromSession` 的「`AgentCtxSeq` 兜底」推出，而它靠 `session_agent_bindings` 回填 —— 绑定原先**只在回合结束**时写。**为什么必须保留的这一条**：与 G-Q 的空 id 守卫是同一块改动面 —— 占位行 `agent_session_id=''`，靠 `upsertExternalSessionNameUnsafe`（空 id 直接 return）和 `lookupSessionAliasForAgentUnsafe`（空 id → not-found）挡住，否则两个都没跑过的同 agent 会话会按空 id 串成同一个别名。合上游时两者要么一起留、要么一起弃。占位行的 `agent_ctx_seq` 必须是 0：非 0 会被 `prependSwitchHint` 当成「已同步到此行」而吞掉 agent 切换提示 |
| 多绑定会话取 ctx_seq 最大者（2026-10-07） | `server/internal/session/types.go:InferAgentFromSession` | **可见症状**：工作台上「大多数用 dsh 的任务没有 agent 徽标」。中途换过 agent 的会话有**两行**绑定，旧实现只处理 `len(AgentCtxSeq) == 1`，对双绑定一律返回空串 → `AgentIcon` 落回「AI」文字占位。而 dsh 恰恰都是在已有 claude 会话里中途切过去的，必然双绑定，所以「大多数 dsh」正好全是这种。判据用 `agent_ctx_seq`（会话的行数游标，`UpdateAgentState` 写的是 `len(Exchanges)`）：谁最大谁最后写过这一串 exchange。**平局（两个都还是 0，即切换后第一轮还没跑完）按名字定序** —— 不定序的话 map 遍历顺序会让同一个会话的徽标在两次列表刷新之间来回跳。只影响 meta-only 的列表路径：其它调用点都加载了 exchanges，走第一个分支（最后一轮 exchange 的 agent），行为不变 |

**针对性测试（防覆盖，改动时同步维护）**

| 测试 | 钉住什么 |
|------|---------|
| `session/manager_test.go` → `TestManagerCreateBindsAgentForListInference` | `Create(Agent:"claude")` 之后**走列表路径**（`List`，从库里重读）断言 `InferAgentFromSession == "claude"` —— 就是徽标那个 bug 本身。注释掉 `Create` 里的写入即变红（已做变异验证） |
| 同上 → `TestManagerCreateBindsAgentWithoutTranscriptID` | 占位行 `AgentSessionID == ""` 且 `AgentCtxSeq == 0`；随后 `UpdateAgentState(...,"real-id")` **覆盖**同一行（行数仍为 1），列表仍报 claude |
| 同上 → `TestManagerEnsureAgentBindingIsIdempotent` | 回合开始的补写不得把已有真实 id / ctx_seq 打回空；换 agent 时老绑定不动、新 agent 立刻有占位行；空 agent 静默跳过 |
| 同上 → `TestManagerEnsureAgentBindingDoesNotLeakEmptyIDIntoAliases` | 空 `agent_session_id` 不得进 `session_external_names`（两个同 agent 的空 id 会话不能串名），`LookupAliasForAgent(agent, "")` 必须为 false |
| 同上 → `TestManagerListInferAgentForSwitchedSession` | 双绑定（claude 40 行 → 中途切 dsh 44 行）**走列表路径**断言 `InferAgentFromSession == "dsh"`。旧实现这条必红（返回空串）。同时断言列表行的 `Exchanges` 为空 —— 否则测不到 `AgentCtxSeq` 回退那条分支，等于没测 |
| 同上 → `TestInferAgentFromSessionIsDeterministicOnTie` | 两个绑定都是 0 时同一份数据反复推断必须报同一个 agent（不能随 map 遍历顺序跳），且不得落回空串 |
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
| `server/internal/api/ws_test.go` → `TestReplayAfterClearYieldsEmptySnapshot` | 回合清空后重新挂载拿到的是**空快照**（`collectReplayStep` 无事件且 `live`），不是上一轮的尾巴 |
| `web/tests/session-overlay-unit.test.mjs` | overlay 的**行为**：`computeTailOverlay` 的 7 个分支。含 4 条【红】②③④⑭（A2 渲染空洞 / aux 与窗口脱节时卡片两边都不渲染 / 短回复落盘后显示两遍 / 只差空白的短用户消息显示两遍） |
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

### 3.1 补全分组（2026-10-06 新增，20 组）

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
- **刷新的 in-flight 去重（2026-10-07）**：可见症状是**每次切项目都打两个 `GET /api/pins`**
  （20 分钟实测 30 次）。根因是两个 effect 同时触发 —— `App.tsx` 依赖 `currentRootId`、
  `SessionList.tsx` 依赖 `selectedRootId/selectedNodeId`，切项目时两者在同一次提交里都变。
  `refreshPinsFromServer` 用模块级 `refreshPinsInFlight` 把并发调用合并成一个 Promise
  （`finally` 里清空，所以结算后再调用仍会重新发 —— 去重不是缓存）。
  必须保留的理由：这不是「少发一个请求」的微优化，它直接决定「切项目时列表闪不闪」。
  针对性测试：`web/tests/pins-refresh-dedup.test.mjs`（并发只发一次 / 结算后能再发 / 仍走 controlPath）。

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
- **卡片角上的 agent 徽标（2026-10-07）**：可见症状是「工作台上大多数用 dsh 的任务没有 agent
  徽标」。卡片读的是 `sessionByKey[main_session_key].agent`，而 `sessionByKey` 只装**当前
  root** 的会话 —— 工作台列的任务大多不属于当前项目，传它进去等于所有跨项目卡片都落回
  `AgentIcon` 的「AI」文字占位。改为传 `boardSessionByKey`（`App.tsx`）=
  `multiProjectSessionGroups` 的会话 ∪ `sessionByKey`，**当前 root 覆盖**。
  刻意不新增第二条扇出：多项目分组本来就把跨项目/跨节点的会话拉全了，并进来即可。
  另一半在服务端 —— `InferAgentFromSession` 对双绑定会话返回空串（见 G-Q），
  两处都得在，只修一处徽标仍是「AI」。
  守卫：`web/tests/workspace-board.test.mjs` 第 13 段（断言传的是合并表、**不是** `sessionByKey`，
  且 `WorkspaceTaskRow` 保留 `main_session_key` 兜底 —— 工作台从不拉任务详情）。

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
- 边界：`ship` 一条命令完成门禁→提交推送→本机编译安装→推送产物到 WSL→WSL 重启→对账。
  `73db73e` 修的是「不再整目录删除 web 哈希资源」。合上游后 `Makefile` 的本地 target 要保住。
  **WSL 是纯 worker，只收二进制与两个 json，不收 `web/dist`**（2026-10-07）：worker 按设计不服务
  静态资源（`GET /` 403），前端只装本机；曾推的那份是留给「角色翻成 control」的保险，已不成立。
- 可见症状（没有它会怎样）：
  - **改了根级文档跑完 ship 却没被提交**：`git add -A --` 的路径是**白名单**，
    `docs/` 只够得到 `docs/` 目录、够不到仓库根。改了 `CLAUDE.md` 再 `deploy-all.sh -m "..."`，
    它会被静默留在工作区，版本号因此挂上 `-dirty`（`git describe --dirty`，2026-10-07 实测）。
    白名单里的 `|| git add -A` 兜底**实际永不触发**：`git add -A -- <存在的路径>` 恒成功。
  - **部署脚本按上游布局整目录删 web 哈希资源**：会让部署前就打开的页面懒加载 404。
- 为什么必须保留：白名单是**故意**的（防 `.mindfs/` 运行期数据被卷进提交），
  所以修法是「把该收的根级文档逐个点名」，不是换成 `git add -A`。
  根级文档中 `CLAUDE.md` / `README.md` / `README.zh.md` / `release-notes.md` 必须在内；
  `config.json` **刻意不在**——它会被本机改 `role`/端口，提交等于把本地运行配置推上去。
- 针对性测试：
  - `web/tests/deploy-web-assets.test.mjs` — 无 `rm -rf web`、TTL 两处一致、WSL 只收产物不收 `dist`、
    add 白名单含根级文档且不含 `config.json`。

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
- **但这张投影表不是「只留 id」**（2026-10-07 修正）：`create_worktree` / `worktree_path` /
  `worktree_built` / `worktree_missing` 四个字段**必须留** —— 卡片上的 worktree 徽标就靠它们推
  （`TaskCardRows` 的 `worktreeTagState`）。实测「工作台所有任务都显示没有 worktree」就是投影把
  它们丢了，而当时这里的注释还断言「工作台对 worktree_* 一个读取都没有」，是错的。
  加字段时同步看 `http_tasks_overview_test.go` 的第 ③/④ 段：③ 列的是**必须丢**的，
  ④ 列的是**必须留**的 —— 往 ③ 里塞 worktree 字段就是把这个 bug 写回去。
- 两个 responder，共用同一段协商逻辑（`writeJSONWithETag`）：
  - `respondJSONList` = **瘦身 + 协商**，只给真正的列表端点（会话列表单项目/`multi_root`、任务总览）。
  - `respondJSONConditional` = **只协商、不动载荷**，给「载荷本身就是详情」的端点：
    `/api/agents`（59KB × 98 次/h）、`/api/task-templates`（24.8KB × 161 次/h）、
    `/api/git/status`、`/api/tree`（7.7KB × 161 次/h）、`/api/tasks`（看板列表，单任务带 stage_runs/events
    流水，实测三段的单任务就 53KB）、`/api/replying-sessions`（2.9KB × 508 次/h）。
    这些端点里 `dirty_count: 0`、空数组、`stages`/`events` 都是承重信息，套上瘦身就是静默丢数据
    （看板详情面板直接读列表项里的 `task.stages`，不是单独拉详情）。内容不变时回 304 零字节。
  - **`/api/sessions/{key}`（单会话详情）** 也接上了 —— 它是全站最重的端点，
    但 2026-10-07 之前没人量过：窗口化尾部拉取（`?latest=20`）单次 **1.14 MB / 1.28 MB**，
    客户端按 ~1.7 秒一次轮询，本机 60 分钟量到 4,746 次、**合计约 1.1 GB/h**
    （占全部请求的 **70%**，而整个优化工程当初针对的基线才 84 MB/h）。
    会话没在跑时这些回包**逐字节相同**（连续四次 sha1 一致），所以 304 直接压到零字节。
    只协商不瘦身：载荷里的 `exchanges` / `exchange_aux` / `window_meta` 少一个键就是少一段对话，
    且 `seq` 增量的 `appendSessionDelta` 判据会被删空键破坏。
  - 新增 `web/src/services/api.ts` 的 `conditionalRequestMax` 从 16 提到 **64** —— 带 ETag 的端点
    从 3 个变成 10 个，其中 `tree?dir=` / `tasks?<filters>` / 会话详情的 `latest=`·`seq=`
    让 URL 空间大一个量级，16 条会把 `/api/sessions`（111KB）这类高频条目挤出去。
    **但仍是有界的**，理由见 G-AH。
  - 条目数**管不住内存**，所以另加 `conditionalRequestBytesMax`（6 MB）字节预算：
    一条会话详情就是 1.14 MB，64 条能把标签页撑爆 —— 而标签页崩溃正是这一轮要修的症状。
    字节数从 `parseProtectedJSONResponseWithSize` 拿（`Content-Length` 拿不到：服务端一次性
    Write 之后是 chunked，实测端点无此头；两条分支本来就把文本读进来了，量长度是免费的）。
    淘汰**必须同时看条数和字节**，且必须把 ETag 与载荷**一起**删 —— 只删载荷会留下
    「304 但拿不出内容」的悬空条目，正好落进 `conditional cache miss` 分支让调用方白重试一次。
- `stripEmptyJSONValues` 改为**返回副本**：原先原地删键，于是「调用方必须传本请求现造的结构」
  成了一条只写在注释里的契约，谁传了共享缓存，那些键就在下次响应里永久消失。
- **两处「静默无效」的坑**（都是 2026-10-07 在 WSL 上量真机字节数才发现的，只看状态码/ETag/304
  全都正常）：
  - 具名容器（`[]map[string]any`、`map[string]map[string]any`…）匹配不到类型开关，落进 `default`
    原样返回 —— **整个 `items` 数组一个键都没省**（`handleSessions` 传的正是 `[]map[string]any`）。
    改用反射统一处理同构容器。反射路径必须跳过 `IsNil()` 的切片/映射：`MakeSlice`/`MakeMapWithSize`
    会把 `null` 变成 `[]`/`{}`，那是**改语义**而不只是省体积。
  - 具名指针（`*time.Time`）同样是漏网：接口里的 typed nil **不等于** nil
    （`any((*time.Time)(nil)) != nil`），`case nil` 抓不到，编成 JSON 的 `null`
    —— `pinned_at` / `archived_at` / `closed_at` 全是这个形态，修完具名容器后**仅剩**这三个字段。
    判空因此补了 `isNilTypedValue`，只认具名 nil，不认零值（`&time.Time{}` 编出来是
    `"0001-01-01…"`，那是真值）。
  - 验证方法就是量字节：会话列表 19 条实测 21 键/条 → **11 键/条**、9,975 → 7,614 字节。
- **别踩**：客户端必须**先判 304 再判 `response.ok`** —— 304 的 `ok` 为 false，顺序写反会把命中
  缓存的响应当成失败。合上游时为「列表响应瘦身 + 条件请求」这一整块，要么全留要么全弃。
- 针对性测试：`web/tests/conditional-request.test.mjs` 逐个钉住六个详情端点走的是
  `respondJSONConditional` **而不是** `respondJSONList`（只断言「接了协商」是不够的 ——
  日后被换成瘦身版照样绿，那正是丢数据的那次改动）。
  `server/internal/api/helpers_jsonlist_test.go` 钉住服务端边界：`0` 不许被当空删、
  数组元素不许被删（位置语义）、入参不许被原地改、**具名容器与具名 nil 必须被递归剥掉**
  （`TestStripEmptyJSONValuesHandlesTypedContainers` / `TestStripEmptyJSONValuesDropsTypedNils`）。
  `conditional-request.test.mjs` 同时钉住单会话详情**必须**走 `respondJSONConditional`
  （既不能退回无条件 `respondJSON`，也不能被换成 `respondJSONList`），以及客户端
  条目数 + 字节预算两条线、ETag 与载荷同生共死。
  `server/internal/api/http_tasks_overview_test.go` 钉住 `/api/tasks/overview` 投影的**两侧边界**：
  ③ 段列「必须丢」（`labels` / `worktree_branch*` / `worktree_root_id` …），④ 段列「必须留」——
  `create_worktree` / `worktree_path` / `worktree_built` / `worktree_missing` 四个 worktree 字段
  必须在响应里且带值（卡片徽标读它们），并断言投影后体积不到全量的一半。
  **只断言「变小了」是不够的**：把投影改成原样下发 `items` 会绿，而把 worktree 字段丢回 ③ 段
  也照样绿 —— 那正是 2026-10-07 那次「工作台所有任务都没有 worktree」。
  **2026-10-07 第二次修正**：`stages` / `current_stage_status` / `aux_flags` 从「必须丢」
  挪到了「必须留」（G-BC）。理由与守卫见 G-AN 末尾与 G-BC 开头 —— 那份「必须丢」清单
  是手抄的，而手抄清单正是漂移的成因。现在另有一条**从消费者源码抽字段**的守卫
  （`http_tasks_overview_card_parity_test.go`），它不受清单写法影响。

### G-AO 自动重载观测

- 来源：`web/src/services/reloadObserver.ts`（新增）、`web/src/main.tsx`（最早一行调用）。
- 边界：**只记录，不干预**。「标签页崩溃 / 自动重载」是最初报告的主要故障形态，前几轮优化打的是
  它的**假设**根因（请求风暴、无界缓存、超线性渲染），但始终没有一条现场证据说明重载本身来自哪里；
  已排除的只有 `staleAssetRecovery` 成环。这段代码的产出就是那份证据：
  - `navType`（`reload` / `navigate` / …）+ 累加的 `loads` → 区分「有代码主动 reload」与「用户重进」；
  - `performance.memory` 峰值（每 30s 采样，仅 Chromium）→ 逼近几百 MB 说明是 OOM 杀页；
  - 本轮第一条 `error` / `unhandledrejection` 的**截断消息** → 区分崩在渲染还是别处。
- 可见症状（没有它时会怎样）：重载反复发生却查不出原因，只能继续按猜测逐项优化，
  改错了也没有任何反馈。读法：控制台 `__mindfsReloadReport()`。
- **别踩**：观测器自己不能成为新的无界增长源（G-AH 修过这个问题）—— 采样上限 10 条、
  错误文本截断 200 字符、只存 sessionStorage（关标签页即清）。存储不可用（隐私模式）时必须
  静默降级、绝不抛异常，否则观测代码会把页面本身弄崩。
- 针对性测试：`web/tests/reload-observer.test.mjs`（累加与上限、峰值只增不减、
  只记第一条错误且截断、隐私模式不抛、存储垃圾不炸）。

### G-AP worktree 收尾按钮重做（机械优先 + 四分支分流 + 幂等 + 真实状态）

- 来源：`server/internal/kanban/{service,worktree_finish,worktree_finish_stage}.go`、
  `server/internal/gitview/worktree_finish.go`、
  `server/internal/api/{http_tasks,http_tasks_finish_teardown}.go`、`server/app/workspace.go`、
  `web/src/components/{TaskCardRows,TaskDetailPanel}.tsx`、`web/src/app/{taskIcons,useRealtimeEvents}.ts(x)`、
  `web/src/services/{tasks,task/worktree}.ts`、`web/src/App.tsx`、`web/src/i18n/locales/{zh-CN,en-US}.ts`。
- 边界：收尾按钮的**给法**（只要有没有合并的 worktree 就给）、**点击行为**（四分支分流 + 幂等）、
  **徽标状态**（目录消失 = 已收尾）三件事合为一组 —— 它们共享同一个判据（worktree 的真实状态），
  合上游时要么全留要么全弃。
- 可见症状（没有它会怎样）：
  - **收尾一直转、点不动、也结束不了**：agent 那半已合完、只差机械清场时，后端无条件调
    `BeginFinishWorktree` 撞「该任务已在收尾流程中」409，前端把按钮换成转圈 —— 用户唯一的出路被藏起来。
  - **worktree 目录已删但徽标仍显示绿色「已开启」**：上游徽标只看 `create_worktree` 字段，
    不看目录是否存在，界面谎称「有 worktree」。
  - **收尾中按钮被禁用**：上游在 `finishActive` 时禁用收尾按钮，而那一刻恰恰是机械清场唯一有意义的时刻。
- 为什么必须保留：上游的收尾按钮是无条件调 `BeginFinishWorktree` 的，徽标只看字段、收尾中禁用按钮 ——
  三点全是上游行为，合上游时会被静默覆盖。
- 三条判据（用户 2026-10-05 拍板）：
  1. **按钮给法**：`create_worktree=true` 且目录存在且目录未消失 → 给；与任务状态（进行中/完成/待审批）无关。
  2. **点击行为**：① 会话在回复 → 200 `session_running`（不做事）；② 分支已合进主干 → 当场机械清场；
     ③ 已有收尾段 → 重跑那段（nudge）；④ 其余 → 追加收尾段。四分支皆幂等。
  3. **徽标状态**：目录消失 = 已收尾（金标准）；目录存在 + 收尾段在跑 = 收尾中；目录存在 + 无收尾段 = 已开启。
- **收尾中的视觉信号是文字，不是动效**（2026-10-07 用户要求）：绿色 worktree 标签在收尾期间文字变
  「收尾中」（`task.worktreeFinishingLabel`），**不加脉冲动画**；详情面板里原先那条转圈横幅
  （`TaskDetailPanel` 的 `finishActive` spinner）一并删掉。转圈是「点不动」的旧症状的残留，
  用户明确要求去掉 —— 按钮全程可点，区分 enabled 与 finishing 的唯一信号就是标签文字本身。
- **会话运行状态是金标准**（不是 `task.Status`）：任务可以长时间停在 `running` 却早就没 agent 了
  （agent 进程死了、状态没人清），旧判据下那种任务**永远收不了尾**。探针 `sessionRunningProbe`
  由 api 层在装配时挂上（`WireSessionRunningProbe` → `StreamHub.IsSessionReplying`），
  没装配时回落到旧状态判据（行为与改造前逐字一致）。
- **第二轮：机械优先 + 完整清场**（2026-10-07 用户拍板，见 `docs/upstream-customizations.yaml` 的 G-AP 条目）：
  收尾是「agent + 机械清场」两个阶段的整合，但很多情况下 agent 那半已经没活可干了。
  现在先做一次**只读**判定（`PlanFinishWorktree` + `gitview.CanMergeCleanly` 用
  `git merge-tree --write-tree` 干跑），能机械清场就直接做掉。判据只有两条：
  worktree 里没有未提交的改动、合并不会冲突。**主 checkout 干不干净刻意不参与判定**
  （用户：「只要机械合并能成就行」）—— 真不干净时 `MergeBranch` 自己拦下并列出文件，
  提前拦只会把「主 checkout 有改动」误报成「要跑 agent」，白等一轮问题还在。
  不能机械清场时（有未提交的活 / 合并会冲突）才走 agent，且把 `reason` + `files` 回给前端。
  **拆目录前必须搬附件**（`MigrateWorktreeUploads`，只搬 `.mindfs/upload/`，主 checkout
  已有同名文件时不覆盖 —— 那份是权威的）。**目录本体整个删**（`RemoveWorktreeDir`，
  用户：「强删 rm -rf」）；但**别人的**孤儿目录仍然只列不删 ——
  `RemoveToolStateOnlyOrphanDirs` 只删「里面只剩 `.mindfs/ .omc/ .claude/`」的空壳，
  含用户文件的目录绝不自动删（`wt-finish.sh` 有同一个 guard）。
  **幂等**：重复点击第二次必须 200 且带一句 `note`。幂等刻意放在 api 层编排
  （`FinishWorktreeAndRepoint`）而不是下层 `FinishTaskWorktree` —— 下层对空路径保持严格报错，
  那是为了守住「先清归属再拆目录」的顺序不变量（路径空 + 目录还在 = 顺序反了，
  静默成功会把它藏起来）。
- **第三轮：机械清场失败落回 agent + 脏判据收窄**（2026-10-08 用户实测）：
  用户重启服务后点 task-37 的收尾，拿到 409「主 checkout 有未提交改动，先提交或暂存后再收尾」
  +「合并会覆盖这些改动，git 不敢替你决定丢不丢。处理完再点一次收尾即可。」，
  涉及 `web/tests/e2e-pending-probe.mjs`、`web/tests/e2e-pending-probe2.mjs` 两个未跟踪文件。
  用户原话：「不应该报错，而是应该把 comment 提交给 agent，进入 agent 合并流程」。
  两处根因 + 一处兜底：
  1. `MergeBranch` 的「已是祖先就短路」排在脏检查**之后** —— 没有东西要合时主 checkout
     脏不脏完全无关（一步 checkout/merge 都不会发生），排在后面就让「分支早就合完了、
     只差拆目录」这种最常见的收尾被一个不相干的改动挡死。
  2. 脏检查把**未跟踪文件**也算进来 —— 过宽。合并只在「要写入同名路径」时才碰得到它，
     那一刻 git 自己会拦（`untracked working tree files would be overwritten by merge`），
     `git checkout` 也一样会拦（两条命令都没带 `-f`，不存在静默覆盖）。收窄成
     `trackedDirtyPaths`（只算已跟踪文件的改动）。
  3. **机械清场失败不再报错，落回 agent**（`agentFallbackPlan`）：机械那条路是**加速段不是
     门槛**，判定说能做、执行时被挡下时结论是「让 agent 去做」而不是「收尾失败」——
     agent 能 commit、能解冲突、能判断哪些改动该留。只有「没有 agent 段可继承」时
     （`BeginFinishWorktree` 也会拒绝）才回 409 + 结构化清单，并把 `agent_error` 一并带出，
     前端在弹窗里说明「为什么不让 agent 处理」。
  顺带修掉一个 porcelain 解析 bug：`strings.Index(line, " ")` 在状态带前导空格时
  （` M foo`）切出带状态前缀的假路径，`dirty_files` 报的是「M note.txt」而不是
  「note.txt」。porcelain v1 每行固定是「XY<空格>路径」，XY 恰好两个字符。
- **第四轮：收尾要能越过「停在 waiting_user 的 agent 段」**（2026-10-08 用户实测 task-9）：
  用户点「日程管理 / 组件库」的收尾，拿到一个巨长的 409：标题「合并已成功，但 worktree
  里还有没提交的东西」+ 12 个文件的清单。用户的原话是「不应该给我报错，而是应该去插入
  comment prompt 给 ai 去执行啊，避免直接报错是我的要求」。三件事叠在一起：
  1. 分支已合进主干（merge 在 21:41）；
  2. 但 worktree 里还有 12 个文件、161 增 115 删（23:30–00:41 做的），**根本没进主干**；
  3. 当前段是 **agent 段的 waiting_user**（agent 干完了但没输出完成标记）。
  三处修：
  1. **`PlanFinishWorktree` 的判定顺序反了** —— 「分支已在主干 → 机械」排在「worktree
     里还有没提交的活」**前面**。分支合过不等于树里没活（上面第 2 点），于是判定说
     「只剩清场」，而拆目录会把那些活一起删掉。下层 `FinishTaskWorktree` 会拦住，但上层
     已经说了错话：白试一次清场，还把那份失败当成主报错抛给用户。**脏检查必须排在前面**
     （拆目录不可逆）。
  2. **收尾段追加不进去** —— `AddStage` 走的是**评论**那条规则（`canAdvanceFromStage`），
     而 agent 段的 waiting_user 在它那里是明确拒绝的，于是用户点了收尾却拿到「这一段没
     走完，追加评论不能替代完成本段」—— 而他点的本来就不是评论。仓库里早有正确的规则：
     `canLeaveStageOnRequest`（注释写着「要放行只有用户手动点」），「下一段」按钮
     （`moveRelative`）用的就是它。新增 `AddStageInput.ByRequest`，收尾置位后走那条规则。
     **放宽的只有这一条路**：不置位时一句评论仍然越不过 waiting_user 的 agent 段。
  3. **两条路都断时报错了对象** —— 响应里塞的是机械清场的报告（`user_changes` = 12 个
     文件），前端按「哪个清单非空」挑弹窗，于是渲染成「合并已成功，但 worktree 里还有
     没提交的东西」+ 一长串清单，而真正的拦路虎在 agent 那条。现在**主 error 用 agent
     那条**（「任务此刻为什么不答应」的直接答案），机械那份只留在 `report` 里当上下文，
     **不把文件清单提到顶层**。报错了对象比不报还糟：用户会照着错的原因去动手。
  顺带删掉上一轮加的 `agent_error` / `note` 管道 —— 它只有一个产出点（就在上面第 3 点
  那个分支），主 error 换成 agent 那条之后它永远等于 `error`，整条管道成了死代码。
- 针对性测试：
  - `server/internal/kanban/worktree_finish_dispatch_test.go` — 探针优先于状态、分支合并判断（真 git）、
    收尾段索引、目录消失读「已收尾」、**分支已合但树里还有未提交的活 → 仍然交给 agent**。
  - `server/internal/kanban/worktree_finish_test.go` — 目录整个删（活 worktree / 空壳两种形状）、
    附件迁移（搬过去 / 不覆盖已有 / 没有 upload 时是空操作）、只删工具状态空壳孤儿、
    顺序不变量（先清归属再拆目录）、**主 checkout 有已跟踪改动时停、未跟踪文件不挡路**。
  - `server/internal/kanban/worktree_finish_stage_test.go` — 收尾段跑完才清场、
    清场自己会清路径（顺序反了会静默失败）、**收尾段能越过停在 waiting_user 的 agent 段、
    而一句评论仍然越不过（放宽的只有 ByRequest 那条路）**。
  - `server/internal/gitview/worktree_finish_test.go` — `CanMergeCleanly` 四种结局
    （干净 / 冲突带文件 / 已合入 / 不碰仓库）、`RemoveWorktreeDir` 拒绝活 worktree、
    `MigrateWorktreeUploads` 不覆盖已有目标、**已合入分支跳过合并（主 checkout 脏也跳过）、
    未跟踪文件不挡合并、真撞同名未跟踪文件时 git 自己拦且文件不丢、`trackedDirtyPaths`
    只报已跟踪改动**。
  - `server/internal/api/http_tasks_begin_finish_test.go` — 五分支分流
    （session_running / teardown / nudged / stage_added / 无 agent 段直接清场）、
    机械优先（干净 worktree 直接 teardown 不追加收尾段）、冲突与未提交都退给 agent 且带
    `plan.files`、连点三次不堆收尾段、机械路径连点两次只合一次且第二次带 `note`、
    **机械清场被挡时落回 agent（200 + stage_added + plan.files）、没有 agent 可继承时才回
    409 + dirty_files、分支已合入 + 主 checkout 有未跟踪文件时照样当场清场、
    停在 waiting_user 的 agent 段也照样交给 agent（200 + stage_added，不再报错）、
    两条路都断时主 error 是 agent 那条且顶层不带任何文件清单**。
  - `web/tests/worktree-badge.test.mjs` — 徽标四态（enabled / finishing / finished / none）、
    目录消失读「已收尾」、无转圈、收尾中不脉冲（区分信号是标签文字「收尾中」）。
  - `web/tests/worktree-finish.test.mjs` — 按钮给法（终态 + 收尾中都不拦截）、无转圈、`hasAgentStage` 门。
  - `web/tests/task-button-gates.test.mjs` — 收尾中收尾键仍给。
  - `web/tests/task-board-view.test.mjs` — 已收尾态 tooltip。

---

### G-BC 看板 / 工作台卡片统一（投影补齐 + 收尾键门控 + 完成键出口 + 收尾失败单通道）

- 来源：`server/internal/api/{http_tasks,http_tasks_finish_teardown,http_tasks_overview_test,http_tasks_overview_card_parity_test,http_tasks_begin_finish_test,http_tasks_finish_test}.go`、
  `server/internal/kanban/worktree_finish.go`、`server/internal/gitview/worktree_finish.go`、
  `web/src/components/task/TaskCardRows.tsx`、`web/src/components/task/TaskDetailPanel.tsx`、
  `web/src/components/workspace/WorkspaceTaskRow.tsx`、`web/src/components/shell/Toast.tsx`、
  `web/src/services/task/{worktree,types}.ts`、`web/src/services/net/error.ts`、
  `web/src/app/useRealtimeEvents.ts`、`web/src/App.tsx`、`web/src/i18n/locales/{zh-CN,en-US}.ts`、
  `web/tests/{task-card-parity,task-button-gates,task-board-view,task-panel-unification,task-stage-panel,worktree-badge,worktree-finish}.test.mjs`。
- 边界：**看板卡与工作台卡是同一张卡**这件事的全部含义 —— 数据（overview 投影带哪些字段）、
  门控（收尾键 / 完成键的判据）、以及收尾失败的上报通道。四块互斥于其他组：
  G-AN 管投影的**体积**边界（哪些必须丢），本组管投影的**完整性**边界（卡片读的必须全在）；
  G-AP 管收尾按钮的**点击分流**，本组管它的**给法**与失败上报。
- **可见症状**（没有这一组时会怎样，逐条对应用户 2026-10-07 的五条反馈）：
  1. 「日程管理#1 既没有运行也没有完成按钮」—— 收尾段卡在待审核时 `showAdvance` 恒假
     （收尾段是流水最后一段），而 `canComplete` 里有一条 `finishActive && canFinishWorktree`
     的豁免，把完成键也一起收走了。那张卡只剩一个收尾键，而收尾键最容易失败。
  2. 「点击收尾提示主 checkout 有未提交改动，过于详细且复杂，还重复显示两条红色报错」——
     同一次失败走了**两条通道**（HTTP 409 给点击者，WS `task.finish_teardown` 又给一次，
     而点击者自己也连着同一个 hub）；错误文案把整份文件路径清单拼进一句话；
     错误码挂成 `file.write_failed`（那些动作一个字节的文件都不写）。
     **第二轮（同一天）**：另一条超长报错 —— 「合并已成功，但拆除 worktree 失败：
     <path> 里还有没提交的东西（<30 个文件拼成一句>）…工具自己留下的临时目录
     （.claude/ .omc/ .mindfs/）不用你管，那部分会自己清」。那是 worktree 里还有用户
     没提交的东西、git 拒绝拆目录，而旧写法把 `strings.Join(UserChanges, "、")`
     拼进一句话。用户原话：「收尾还有这种超长报错，系统性查一下简化处理一下」。
  3. 「工作台有 worktree 的卡片但是没有收尾键」—— 判据里有一条 `has_agent_stage`，
     而「有 worktree 但没有 agent 段」的任务恰恰最需要收尾键（没有 agent 去 commit，
     只能服务端直接机械清场）。服务端此前也确实对这种任务回 409。
  4. 「看板的卡片有专属小图标，工作台的没有，我都不知道什么意思」—— overview 投影
     丢了 `aux_flags` / `stages` / `current_stage_status`，于是工作台卡一个徽标都画不出、
     推进键判据恒假。
  5. 「这几个修改我曾经提到过多次，为什么前面没完成」—— 见下面「根因」。
- **根因（为什么前面没做完）**：overview 投影的字段清单是**手抄**的，而手抄清单的采集范围
  是 `grep useWorkspaceBoard.ts 与 components/workspace/*` —— 漏了真正的消费者
  `components/task/TaskCardRows.tsx`（工作台与项目看板**引用同一个组件**，commit `0792221` 起）。
  每次修复都按症状补一个字段（先 worktree 四字段，再 `has_agent_stage`），没人回到消费者
  重推清单。而丢字段**不会报错**：`hasLaterStage` / `isFinishStageActive` / `canAdvanceCard`
  在 `stages` 缺席时一律返回 `false`，`auxFlags` 缺席时徽标数组为空 —— 没有断言、没有日志、
  测试全绿，只有用户看得见。再加一条：`useWorkspaceBoard.liveVersion` 会按 `updated_at`
  用完整任务覆盖投影，所以同一张卡在工作台可能是全字段、也可能是投影，进一步掩盖了缺口。
- **因此本组的核心不是「加字段」，是加一条不受清单写法影响的守卫**：
  `server/internal/api/http_tasks_overview_card_parity_test.go` 从 `TaskCardRows.tsx` 与
  `appTask.ts` 的**源码里抽出所有 `task.<field>` / `stage.<field>` 读取**（减掉 `t("task.x")`
  那些 i18n key、减掉 `stage.snapshot` 那个模板段字段），逐条断言投影带；反向再断言投影
  **不多带**前端零读取的键。组件多读一个字段而投影没加 → 红。这条守卫做过变异验证
  （临时摘掉 `CurrentStageStatus` → 立刻红）。
  前端那一半是 `web/tests/task-card-parity.test.mjs`：把同一份任务数据喂给两个渲染入口，
  钉住按钮集合、徽标文案、`hideSessionError` 口径、收尾键判据两侧一致。
- **投影新增三个字段**（`overviewTaskProjection`）：
  - `Stages []overviewStageProjection` —— `StageTemplate` 的**卡片视图**，只留
    `name` / `role` / `kind`。`prompt_template` 是体积大头（单任务 14.4%），卡片一个字节都不读它。
  - `CurrentStageStatus string` —— `canAdvanceCard` 的判据，与详情面板同一口径。
  - `AuxFlags *overviewAuxFlags` —— 指针 + `omitempty`，全空时不出现，免得每条任务都挂一个 `{}`。
  - **`has_agent_stage` 从投影和前端读取里一起删掉**：`stages` 补齐后它是纯冗余字段，
    留着只会让下一个人猜「收尾键到底以 stages 还是以这个布尔为准」—— 那正是本次漂移的成因。
- **收尾键判据只剩 worktree 三态**（`create_worktree` 开着、`path` 在、目录没被删），
  三处一起改：`TaskCardRows` 的 `canFinishWorktree`、`TaskDetailPanel` 的同名判据、
  服务端 `handleKanbanTaskBeginFinish` 新增 ③.5 分支（无 agent 段 → `teardownFinishWorktree`
  直接机械清场，不再 409）。**三处必须一起改** —— 只改一处就是「给一个必然 409 的按钮」。
- **完成键放开**：`canComplete = !terminal && !stageRunning && !showAdvance`，删掉
  `finishActive && canFinishWorktree` 那条豁免。`TaskDetailPanel` 的 `canCompleteTask` 同样放开
  （同一个 bug，留在面板里就是已知的不一致）。
- **终态任务只要 worktree 还在就给收尾键**（用户原话：「只要还有 worktree 就必须有收尾键」）。
  收尾键因此**搬出 `!terminal` 分支**，抽成 `FinishWorktreeButton` 在两个分支各渲染一次 ——
  抄两遍的话，改一处忘另一处就是「两边卡片不一致」的下一个来源。服务端 `Complete` 不清
  `worktree_path`，`BeginFinishWorktree` 的 `reviveTerminalTask` 已支持终态复活，链路是通的。
- **收尾失败单通道上报**：规则是「有 HTTP 响应可带结论时，失败只走 HTTP；没有时（异步）才用 WS」。
  成功**永远广播** —— 所有人都得知道 worktree 没了（前端对 `teardown` 的 200 不说话，
  点击者也靠这条推送拿结论）。异步那条路（`WireFinishStageTeardown`）没有 HTTP 响应可带，
  成败都只能广播，不受这条规则影响。
- **错误文案简化 + 清单结构化**：四个类型化错误，`Error()` 只留一句人话，文件清单走
  结构化字段给 UI 列表渲染 —— **清单不进句子**。这一条是用户两轮实测逼出来的：
  把 `strings.Join(files, "、")` 拼进 `fmt.Errorf` 的话，三十个文件就是一句读不完的话，
  而句子长了用户根本不会读完，等于没说。
  - `gitview.MainCheckoutDirtyError{Files}` + `kanban.FinishWorktreeDirty{Files}`
    —— 「主 checkout 有未提交改动，先提交或暂存后再收尾」，清单走 `dirty_files`。
  - `kanban.FinishWorktreeUserChanges{Files}` —— 「合并已成功，但 worktree 里还有没提交的
    东西，先处理掉再收尾」，清单走 `user_changes`。**文案必须点明「合并已成功」**，
    否则用户看到「收尾失败」会以为白干了一场。
  - `kanban.FinishWorktreeConflict` —— `Error()` 从「合并撞上冲突，需人工处理：<文件列表>」
    改成「合并撞上冲突，需人工处理」。清单本来就在 `conflict_files` 里给 UI 列表渲染，
    句子里再拼一遍是同一份东西出现两次。
  - 409 载荷 `{ error, dirty_files, user_changes, conflict_files, output, result }`；
    `FinishTeardownReport` 同步加 `DirtyFiles` / `UserChanges`，WS `task.finish_teardown`
    带上 —— 异步那条路没有 HTTP 响应可带，清单丢了就只能报一句没有清单的错。
- **Toast 不再显示错误码那一行**（`error.code` 副文本）。它从来不是给用户看的信息 ——
  用户看到「写入文件失败」只会去查磁盘。任务相关动作的错误码同时归位到新增的
  `task.action_failed`（`error.task.actionFailed` = 「任务操作失败」）；
  `FileOperations.tsx` / `ActionBar.tsx` 那 3 处是真的文件写入失败，**不动**。
- **别踩**：
  - 投影加字段时**先跑** `http_tasks_overview_card_parity_test.go`，再跑
    `http_tasks_overview_test.go` 的 ③/④ 段 —— 后者仍要钉「体积大头必须不在」
    （`prompt_template` / `labels` / `worktree_branch*`），别把瘦身成果还回去。
    测试 fixture 的 `prompt_template` 要足够长，「投影 < 全量一半」那条断言才撑得住。
  - `liveVersion` 会让同一张卡在工作台拿到全字段或投影两种数据。**别在测试里假设
    工作台卡一定是投影** —— 要测投影行为就走 `projectOverviewTask` 那个纯函数。
  - 收尾键判据改动必须**三处同改**（卡片 / 详情面板 / 服务端 ③.5）。只改前端不给服务端
    加 ③.5，无 agent 段的任务点收尾会拿到 409。
- 针对性测试：
  - `server/internal/api/http_tasks_overview_card_parity_test.go` — 投影覆盖共享卡片读取的
    **每一个**字段（从源码抽，不是手抄清单），且不多带前端零读取的键。做过变异验证。
  - `server/internal/api/http_tasks_overview_test.go` — 投影的两侧边界：体积大头必须不在、
    `stages` 每段只许有 `name`/`role`/`kind` 三个键、`aux_flags` 五个字段带值、
    全空时整个键不出现、`has_agent_stage` 必须已删。
  - `server/internal/api/http_tasks_begin_finish_test.go` — ③.5 分支（无 agent 段 → 直接
    机械清场，worktree 真的被拆、`worktree_path` 真的被清）；**去重**（失败时广播 0 次、
    成功时广播 1 次，用 `broadcastFinishTeardown` 那个可替换入口计数）。
  - `server/internal/api/http_tasks_finish_test.go` — 主 checkout 不干净 → 409 + `dirty_files`，
    且消息里**不含**文件名（清单属于字段，不属于句子）。
  - `server/internal/gitview/worktree_finish_test.go` — `*MainCheckoutDirtyError` 类型化、
    `Files` 带文件、消息保持短句。
  - `web/tests/task-card-parity.test.mjs` — 两侧引用同一个卡片组件、`hideSessionError`
    口径一致、收尾键判据两侧一致、完成键判据、终态分支「收尾 + 删除」的顺序、
    aux 五个标记的类型与文案、dirty 短文案与结构化清单、`task.action_failed` 错误码、
    Toast 不渲染错误码、WS 处理器认得 `dirty_files`。
  - `web/tests/{task-button-gates,task-board-view,task-panel-unification,task-stage-panel,worktree-badge,worktree-finish}.test.mjs`
    — 门控判据翻转后的对应断言（收尾中给完成、无 agent 段仍给收尾键、终态分支多一把收尾键）。

---

### G-AQ 任务卡的三把键：完成兜底 / 取消 / 删除（2026-10-06 用户三条要求）

- 来源：`web/src/components/{TaskCardRows,TaskDetailPanel}.tsx`、`web/src/app/taskIcons.tsx`、
  `web/src/services/tasks.ts`、`web/src/App.tsx`、`web/src/components/TaskBoardView.tsx`、
  `web/src/i18n/locales/{zh-CN,en-US}.ts`、`server/internal/kanban/{service,task_store}.go`、
  `server/internal/api/{http,http_tasks}.go`。
- 边界：**任务卡上「非终态三键 + 终态一键」的给法与语义** —— 三件事共享同一组判据
  （`showAdvance` / `canComplete` / `terminal`），合上游时要么全留要么全弃。
- 可见症状（没有它会怎样）：
  1. **已收尾的卡片挂着一个点不动的黄色循环键**：重建 worktree 只在 `worktree_missing`
     （目录已不在）时出现，而目录不在就等于已收尾 —— 收完尾的任务既没有树可执行也没有树可拆，
     「重建恢复后再执行」是一句兑现不了的承诺。上游给的就是那个键。
  2. **任务卡在待审核、界面上没有任何出路**（用户 2026-10-06 实测）：指针停在收尾段上、
     那一段卡在待审核，而 worktree 已经被拆 —— 收尾键给不出来（没有目录可拆）、
     执行键给不出来（收尾中不推进），任务就此永久卡住。
  3. **终态卡片只增不减**：取消改的是状态、卡片还留在终态列里；看板翻历史翻到满屏也清不掉一张。
  4. **取消键戴着垃圾桶**：垃圾桶在这里是句谎话 —— 它删不掉任何东西。
- 为什么必须保留：三条全是上游行为（上游的完成门控是「没有下一段才给」、没有删除端点、
  取消用的是 `TrashIcon`），合上游时会被静默覆盖。
- 三把键的语义（用户 2026-10-06 拍板）：
  1. **完成 = 推进键给不出来时的出口**。`canComplete = !terminal && !stageRunning && !showAdvance
     && !(finishActive && canFinishWorktree)`。**不变式：非终态、非运行中的任务上，
     推进键与完成键至少有一个**。卡片（`TaskCardRows`）与详情面板（`TaskDetailPanel` 的
     `canCompleteTask`）同一套口径 —— 两处判据不同会让同一张任务在两个界面上给出相反答案。
  2. **取消只改状态**（`onMove(task, "cancel")`），卡片留在终态列里还能翻回去看；
     图标是「作废符」（`TaskCancelIcon`：圆 + 斜杠），**刻意不用垃圾桶**。
  3. **删除只给终态任务**，且只删卡片本身：`DELETE /api/tasks/{id}?root_id=` →
     `Service.DeleteTask` → `TaskStore.DeleteTask`（一个事务里删 `tasks` + `stage_runs` + `task_events`）。
     **worktree / 分支 / 会话都不动** —— 那是「收尾」的职责，不是「删除」的。
     正在跑的任务拒绝删除（`assertNotRunning` + `taskRun` 在途标记），否则删了卡片就没人能停它。
     删完广播 `task.deleted`（`AppContext.TaskDeleted`，此前已定义但从未被调用，前端处理早就写好了）。
- **重建 worktree 的入口从 UI 移除**，但服务端 `POST /api/tasks/{id}/rebuild-worktree`
  与前端 `rebuildTaskWorktree` **保留**（脚本 / CLI 显式调用）。移除的只是两个界面上的按钮。
- 针对性测试：
  - `web/tests/task-button-gates.test.mjs` — **不变式**：推进键给不出来 ⟹ 完成必须补上（逐状态 × 逐指针位置）；
    「指针停在收尾段 + worktree 已拆 + 待审核」这个具体卡死形态必须拿到完成键。
  - `web/tests/task-stage-panel.test.mjs` — 面板不再有重建入口；完成/删除键住在 `headerRight`；
    `worktree_missing` 仍是收尾的判据。
  - `web/tests/task-board-view.test.mjs` — 卡片里不再出现 `TaskRebuildWorktreeIcon`；
    `canComplete` 是 `showAdvance` 的反面。
  - `web/tests/task-panel-unification.test.mjs` — 取消在 `!terminal` 分支内、删除在 terminal 分支；
    **取消不得用垃圾桶图标**。
  - `server/internal/kanban/task_delete_test.go` — 卡片 + 从属行一起走、邻居不受影响、
    空/不存在的 id 报错、running 与在途队列拒绝删除、标记摘掉后能删。

### G-AR 移动端侧栏切换按钮移入各视图共用 header

- 来源：`web/src/layout/ViewHeader.tsx`（新增）、`web/src/layout/AppShell.tsx`、
  `web/src/components/{ActionBar,FileTree,SessionList,SessionViewer,GitDiffViewer,DefaultListView}.tsx`、
  `web/src/components/action/types.ts`、`web/src/App.tsx`、`web/tests/task-board-view.test.mjs`。
- 边界：移动端呼出左右侧栏的两个切换按钮，从 ActionBar（footer）搬进**中间主栏**各面板
  共用的 `ViewHeader` 外壳的两侧，合为一组 —— 按钮位置、ViewHeader 的渲染分支、ActionBar
  的 hideComposer 分支共享同一条「按钮在哪」的判断，合上游时要么全留要么全弃。
  侧栏自身（FileTree / SessionList）传 `showToggles={false}`，不显示切换按钮。
- 可见症状（没有它会怎样）：
  - **移动端进看板/工作台后开不出侧栏**：按钮原本只在 ActionBar 的 hideComposer 分支里，
    上游若把该分支改成 `return null`（或删掉移动按钮），这两个界面就再也呼不出侧栏。
  - **按钮多占一行**：早期实现新增了一条 44px 全局顶栏（多一行），用户明确否决 ——
    按钮必须插在各视图**原有 36px header 内部的两侧**，不额外占行。
- 为什么必须保留：上游没有共用 header 组件（6 个视图各写各的内联 36px header），
  也没有移动端侧栏切换按钮；合上游时 ViewHeader 与各视图的接线会各自被覆盖回上游形态。
- 实现要点：
  - `ViewHeader` 提供 36px header 外壳；**桌面端 children 直接渲染（零行为变化）**，
    移动端才在两侧插按钮、children 收进中间弹性容器。各视图用 `style`/`innerStyle`
    复刻自己原有的排布（space-between 等），`padding` 沿用各视图原值。
  - 按钮接线走 `MobileSidebarToggleContext`（AppShell 提供 `toggleRail` + 物理左右栏状态与标签），
    ViewHeader 消费 —— 不逐层透传 prop，因为 header 在 6+ 个视图里、中间层与侧栏开合无关。
  - 侧栏切换按钮 22×22（比原来的 30×44 小），图标 14px。
  - `showToggles` prop 控制是否渲染按钮：中间主栏（SessionViewer / GitDiffViewer /
    DefaultListView）默认 true；侧栏（FileTree / SessionList）传 false。
  - **主栏的空态/覆盖态也要带 ViewHeader**：`App.tsx` 里顶替 `workspaceView` 的两支裸 div
    ——「chat 模式未选中会话」空态、插件信任面板 —— 各包一层 `flex-direction:column`
    容器 + `<ViewHeader />`。它们是整块主区、里面没有任何带 header 的组件，漏了就是
    「移动端停在空态时一个切换按钮都没有」（2026-10-07 实测）。桌面端因此各多一条 36px 空 header，
    与选中会话时（SessionViewer 恒有 header）一致。
  - ActionBar 的 `hideComposer` 分支 `return null`；输入区移动端补 `0 8px` 左右内边距
    （原先靠两侧 30px 按钮占位，按钮搬走后需显式留白）。
- 针对性测试：
  - `web/tests/task-board-view.test.mjs` — ViewHeader 移动端渲染两个 toggle 且接 `ctx.toggle`、
    AppShell 经 context 交出 `toggleRail`、ActionBar `hideComposer` 返回 `null`、
    5 个视图都使用并闭合 `<ViewHeader>`、按钮 22×22、侧栏 `showToggles={false}`、
    **chat 空态带 `<ViewHeader />`**。
  - `web/tests/project-tree-refresh.test.mjs` — FileTree header 换成 ViewHeader 后，
    tabs 容器的 6px marginRight 与 gap:0/overflow:visible 仍共存。

---

### G-AS ACP 提问：dsh 的 `ask_user_question` 走 elicitation（2026-10-07）

- 来源：`server/internal/agent/acp/{elicitation.go,process.go,session.go}`、
  `server/internal/agent/acp/{elicitation_test.go,session_test.go}`。
- 边界：**ACP 客户端侧的 elicitation 通路** —— 能力广告（`Initialize`）→ handler
  （`UnstableCreateElicitation`）→ 提问登记/关联（`pendingAskUserByCallID`）→ 工具卡重标
  （`kind=ask_user` + `meta.questions`）→ 答案编码（`question_<i>`）→ 取消/关闭回 Decline。
  这几件事互为前提（广告了没 handler 是 `-32601`，有 handler 没广告是死代码，
  不重标工具卡则前端不渲染提问卡、`MarkPendingAskUserAnswered` 也会以
  「pending tool call is not ask_user」拒收答案），合上游时要么全留要么全弃。
- 可见症状（没有它会怎样）：在 dsh 会话里让 agent 提问，工具直接返回
  `Error: the ACP client does not support form elicitation`，**提问卡根本不出现**，
  用户没有任何途径回答；模型只能拿到一条失败的工具结果，继续瞎猜。
- 根因（三条缺一不可，2026-10-07 读码定位）：
  1. `Process.Initialize` 只广告 `Terminal: false`，没有 `elicitation.form`
     → openma 适配器 `clientElicitationForm = false`
     → `installAcpUserQuestionProvider` 直接抛 `UserQuestionError("the ACP client does not
     support form elicitation", "CLIENT_UNSUPPORTED")`（`bridge.js`）。
  2. `mindfsClient` 没有 `UnstableCreateElicitation` 方法 —— 就算广告了能力，
     SDK 的类型断言失败，回 `-32601 Method not found`。
  3. `(*session).AnswerQuestion` 是硬编码 `errors.New("ask user question is not supported
     by acp sessions")`，前端提交的答案无处可去。
- 为什么必须保留：上游 ACP 客户端没有 elicitation 通路（上游只有 claude 走
  `AskUserQuestion` 工具卡、codex 走自己的协议），这三条都是本 fork 补的；
  合上游时 `Initialize` 的 capability 结构、`mindfsClient` 的方法集、
  `AnswerQuestion` 的函数体都会被整体覆盖回去。
- 设计要点（合上游后要按这些恢复，不要按上游实现重写）：
  1. **只对 `agentName == "dsh"` 广告 `elicitation.form`**。其它 ACP agent 不广告 ——
     广告只会让它们开始发 elicitation，而 mindfs 无法按 session 精确路由（见 3），
     只能 decline，等于把「本来就不支持」变成「支持但总是失败」。
  2. **`UnstableCreateElicitationForm` 不带 `sessionId`/`toolCallId`**（这个 fork 的
     codegen 把 `schema.unstable.json` 里定义的 `sessionId` 丢了），所以关联靠
     **题目 id + 题目文本逐字相同**（`elicitationQuestionsMatch`）。两边同源于同一次模型
     输出，实测稳定。
  3. **关联要等 2s 窗口**（`elicitationBindWait`）：ACP SDK 对 request 立即开 goroutine
     处理，`session/update` 通知却走队列由另一个 goroutine 顺序处理
     （`connection.go` 的 `receive`/`processNotifications`），所以 elicitation/create
     **可能比对应的 `tool_call` 通知先到**。窗口内按 20ms 轮询。
  4. **刻意不做「只剩一张卡就绑它」的兜底**：并发提问时 A 的卡已登记、B 的 elicitation
     先到，兜底会把 B 的答案投给 A —— **答错题比答不上更糟**。窗口内等不到就回 Decline
     （fail-safe：agent 收到「用户取消提问」）。
  5. **`tool_call` 与 `tool_call_update` 两个分支都要重标** `kind=ask_user` +
     `meta.questions`：`mergeBufferedToolCall` 对 `Meta` 取并集、对 `Kind` 取后者，
     只重标一个的话 update 的 `kind=other` 会把 `ask_user` 覆盖掉，提问卡渲染完又消失。
  6. **答案编码对齐 openma 的 `answerFromElicitation`**：有选项的题
     `question_<i> = "option_<j>"`（j 是该 label 在 `_meta.dsh.userQuestions.questions[i]
     .options` 里的下标），多选是数组；自由文本走 `question_<i>_custom`；
     无选项的题 `question_<i>` 直接是字符串。**单选命中选项时不得再带 `_custom`** ——
     适配器在 `custom` 非空且非多选时会把 `selected` 清空。
     前端提交的键是 `q_<i>`（多选用 `", "` 连接），由 `elicitationContent` 转码。
  7. **两个来源的字段大小写不同，别照抄**：工具卡的 `rawInput` 是**工具参数**，
     多选是蛇形 `multi_select`；`_meta` 里的题目是 **user-questions seam 的对象**
     （`dsh-tool-ask-user` 把 `multi_select` 转成 `multiSelect` 后交给 seam），
     多选是驼峰 `multiSelect`。读错一个字母 → `MultiSelect` 恒 false →
     多选答案被编码成字符串（适配器只认第一个）+ 单选命中选项时被 `_custom` 清空，
     **提问照常出现、答案静默错**。两边归一化后必须逐字相等，否则
     `elicitationQuestionsMatch` 永远匹配不上、提问静默退化成「用户取消」。
  8. **取消与关闭要回 Decline**：`CancelCurrentTurn`（用户点停止）与 `CloseSession`
     按 sessionKey 清；`Process.Close()` 清全部。不回的话 handler 会一直阻塞到连接关闭。
     投递「恰好一次」由 `elicitationMu` 下「先从表里删掉、再往容量 1 的 waiter 投」保证。
  9. `registerPendingAskUser` 是 **first-wins**（同 callID 不覆盖），否则后续
     `tool_call_update` 会换掉 waiter，已绑定的 elicitation 永远等不到答案。
 10. **三条「提问没被回答就结束」的路都必须释放条目**，漏一条就留下一个 bound 条目 ——
     它会被 `matchAndBindPendingAskUser` 优先匹配到（id+文本相同），后续同题的答案投进
     没人读的 waiter，表现为「卡片出现、提交也成功、模型却说用户取消了」：
     ① `AnswerElicitation`（正常回答）：先删后投，恰好一次；
     ② `UnstableCreateElicitation` 的 `ctx.Done()` 分支：handler 自己
     `releasePendingAskUser`（它已在读，不能再投递）；
     ③ `reapPendingAskUser`（工具卡进终态）：**已绑定的条目也要删**，并往 waiter 投一个
     空结果把阻塞的 handler 放出来 —— 进终态说明 agent 已经不等这个答案了。
     正常路径下 ③ 看不到 bound 条目（②之前就被 ① 删了），能看到的都是被放弃的提问。
     实测踩过：第一次实测那轮提问被打断，条目就这么挂着。
 11. **匹配与占位必须在同一把锁里**（`matchAndBindPendingAskUser`）：拆成「先匹配、
     再置 `bound`」两步，会让两个并发的 elicitation（不同会话问了同一道题 —— id 与
     文本都相同）同时匹配到同一条目、一起等同一个 waiter，一个拿到答案、另一个
     只能干等到 ctx 取消。
- 兼容性：`session.AnswerQuestion` 签名不变；`convertEvent` 多一个 `onAskUser` 回调参数
  （测试传 `nil`），对非 ask_user 工具卡是逐字节 no-op；其它 ACP agent 不受影响；
  无数据迁移；能力广告在 `Initialize` 时生效，**进程池里的 dsh 进程重建后才生效**。
- 针对性测试：
  - `server/internal/agent/acp/session_test.go:TestConvertEventRetagsACPAskUserToolCard`
    — 工具卡被重标成 `ask_user`，`meta.questions` 形状正确，回调拿到归一化题目。
  - `server/internal/agent/acp/session_test.go:TestConvertEventKeepsACPAskUserKindOnToolUpdate`
    — `tool_call_update` 分支也必须保持 `ask_user`（防 `mergeBufferedToolCall` 覆盖）。
  - `server/internal/agent/acp/session_test.go:TestConvertEventIgnoresNonAskUserToolCards`
    — 非提问工具卡不被误重标（`kind`/`meta` 原样）。
  - `server/internal/agent/acp/elicitation_test.go:TestElicitationContentMapsLabelsToOptionIndexes`
    — 答案编码：单选命中/单选自定义/多选命中/多选混合/无选项/空答案六种形态。
  - `server/internal/agent/acp/elicitation_test.go:TestBindElicitationMatchesByQuestionIDs`
    — 按题目 id+文本关联；已绑定的条目不会被二次绑定。
  - `server/internal/agent/acp/elicitation_test.go:TestBindElicitationWaitsForLateToolCall`
    — elicitation 先到、`tool_call` 通知晚到也能关联上（SDK 队列时序）。
  - `server/internal/agent/acp/elicitation_test.go:TestBindElicitationGivesUpWhenNothingMatches`
    — 对不上就放弃（钉住「不做唯一卡兜底」这条决策）。
  - `server/internal/agent/acp/elicitation_test.go:TestAnswerElicitationDeliversContent` /
    `TestAnswerElicitationRejectsUnknownCall` — 答案投递与未知 callID 报错。
  - `server/internal/agent/acp/elicitation_test.go:TestCancelAndCloseDrainPendingQuestions`
    — 按 sessionKey 取消、`dropAll` 清空、其它会话不受影响。
  - `server/internal/agent/acp/elicitation_test.go:TestDshElicitationQuestionsParsesMeta`
    — `_meta["dsh.userQuestions"]` 解析（含缺失/空数组）。
  - `server/internal/agent/acp/elicitation_test.go:TestUnstableCreateElicitationAnswersPendingQuestion`
    — 端到端：handler 绑定 → 答案编码 → `Accept.Content["question_0"] == "option_1"`。
  - `server/internal/agent/acp/elicitation_test.go:TestUnstableCreateElicitationDeclinesUnsupported`
    — 非 form 模式 / 无题目 / 无对应卡三种情形都回 Decline（不报错、不挂住）。
  - `server/internal/agent/acp/elicitation_test.go:TestUnstableCreateElicitationCancelOnContextDone`
    — ctx 取消回 Cancel，handler 不泄漏。
  - `server/internal/agent/acp/elicitation_test.go:TestPendingAskUserRegistryConcurrency`
    — 登记/清理/取消并发下不 panic、不残留（`-race` 下也过）。
  - `server/internal/agent/acp/elicitation_test.go:TestAskUserRawInputAndElicitationMetaAgree`
    — **两个来源的大小写契约**：`rawInput`（蛇形 `multi_select`）与 `_meta`（驼峰
    `multiSelect`）归一化后逐字相等；多选答案编码成数组。
  - `server/internal/agent/acp/elicitation_test.go:TestElicitationWireShape`
    — **协议字面量**：客户端能力 JSON 里 `elicitation.form` 必须在（适配器只认这个）；
    适配器形状的 `elicitation/create` 参数能被解析且 `_meta` 不被 SDK 吞掉；
    三种响应序列化成 `{"action":"accept","content":{...}}` / `{"action":"decline"}` /
    `{"action":"cancel"}`。
  - `server/internal/agent/acp/elicitation_test.go:TestACPClientCapabilitiesOnlyAdvertisesElicitationForDsh`
    — **兼容性边界**：只有 `dsh` 拿到 `elicitation.form`，其它 ACP agent 与上游一致。
  - `server/internal/agent/acp/elicitation_test.go:TestBindElicitationIsExclusiveUnderConcurrency`
    — 并发 elicitation 下每个条目最多被绑一次（`-race` 下也过）。
  - `server/internal/agent/acp/elicitation_test.go:TestReapReleasesBoundElicitation`
    — 工具卡进终态时已绑定条目也被删，且 waiter 被放出（防 handler 永久阻塞）。
  - `server/internal/agent/acp/elicitation_test.go:TestReapKeepsEntryWhileRunning`
    — reap 只认终态，`running`/`pending`/`in_progress` 时不能清条目。
  - `server/internal/agent/acp/elicitation_test.go:TestReleasePendingAskUserUnblocksReregistration`
    — ctx 取消释放后，同一道题再问一次能重新登记并匹配到新条目。
  - `server/internal/agent/acp/elicitation_test.go:TestReleasePendingAskUserIgnoresStaleEntry`
    — 拿旧条目去释放不会误删同 callID 的新条目（指针比较）。

### G-AT 会话打开性能（窗口去重 / 载荷压缩 / 工具卡分组）（2026-10-07）

- 来源：`server/internal/session/{types.go,manager.go}`、`web/src/services/{session.ts,pins.ts}`、
  `web/src/app/useRealtimeEvents.ts`、`web/src/hooks/useSessionStream.ts`、
  `web/src/components/SessionViewer.tsx`、`web/src/components/stream/ToolCallGroupCard.tsx`（新增）。
- 边界：**会话打开热路径（取窗 → 载荷 → 渲染）上的七条**，合上游时要么全留要么全弃。
  与 G-AN 的分工：G-AN 管**缓存协商**（同一份载荷如何零字节重取），G-AT 管**载荷本身有多大**
  以及**取几次**。两者叠加才把「打开会话慢」压下来，但任一条单独被冲掉都不会让门禁变红 ——
  所以每一条都有各自的针对性测试。
- 可见症状（没有它会怎样）：
  1. 打开会话卡顿、加载慢：窗口响应 515 KB，`exchange_aux` 占 449 KB（**87.4%**）。
  2. 每个 sessionKey 的 `?latest=N` 在访问日志里**恰好出现两次**（两个并发调用各传一遍全量载荷）。
  3. 20 分钟内 related-files 被拉 **74 次**（每秒 4–6 个突发），`/api/pins` 被拉 **30 次**（每次切项目成对）。
  4. 单会话 389 张工具卡（单 seq 最多 189 张）同步渲染。
- 七条（合上游后要按这些恢复，**不要按上游实现重写**）：
  1. **窗口 in-flight 去重**（`session.ts:getSessionWindow`）：`windowInflight` 把同
     `(nodeId, rootId, sessionKey, beforeSeq, latest, limit)` 的并发调用合并成一个 Promise。
     键**必须含 `nodeId`** —— 同名项目跨节点是两个不同请求，合并等于串数据。
     在途表在 `finally` 里删，所以结算后再调用仍会重新发（**去重不是缓存**）。
  2. **edit 丢冗余 meta**（`types.go:CompactToolCall` + `dropEditRedundantMeta`）：edit **有**
     content（diff）时 `meta.input`（109 KB）/`meta.output`（21 KB）是同一份 diff 的原始形状，
     渲染从不读；edit **没有** content 时必须保留（那才是唯一详情来源）。
  3. **窗口轻压缩**（`types.go:CompactExchangeAuxLight` + `manager.go:loadExchangeAuxWindow`）：
     在 2 之上把 edit/read/execute 的 `content` 也清空。折叠卡片只要 kind/title/status/locations；
     展开时 `ToolCallCard` 的 `needsRemoteDetails`（edit/read 走 `!hasContent`、execute 走
     `!hasExecuteOutput`）会触发 `GET /api/sessions/{key}/toolcalls/{callID}` 懒加载。
     **只用于窗口读路径**：全量 sync / 重锚定仍走 `CompactExchangeAux`、保留 content，
     避免「重锚定后卡片内容闪一下又变了」。这条不一致是**刻意**的：首屏要快，重锚定要稳。
  4. **窗口 20→8**（`session.ts:SESSION_WINDOW_SIZE`）：首屏载荷 87% 是 aux，窗口缩小直接砍载荷；
     代价是上翻同样历史要多几次 `loadMore`，而打开会话是高频路径、翻历史是低频路径。
  5. **related-files 去抖**（`useRealtimeEvents.ts:refreshSessionRelatedFiles`）：后端
     `shared_watcher` 每写一个文件就发一条 `session.related_files.updated`，前端 handler 原本
     每次原样拉一次。按 `rootID::sessionKey` 合并 500ms 窗口内的多次触发，只拉一次。
     触发方改成同步派发（不再是 `async`）：去抖后没有可 await 的即时结果。
  6. **工具卡分组**（`useSessionStream.ts:groupConsecutiveToolCalls` +
     `ToolCallGroupCard.tsx` + `SessionViewer.tsx` 的 `tool_group` 分支）：连续同类、数量 **>= 5**
     的 edit/read/execute 折成一个 `tool_group` 项，折叠态显示「N 个编辑」+ 运行中/失败计数，
     展开才逐个渲染 `ToolCallCard`。**ask_user 不参与**（要答题）；todo/plan/compact 各有卡片。
     阈值 5：2–4 张直接显示更直观，为它们套一层展开/收起反而多一次点击。
  7. **pins 去重**（`pins.ts:refreshPinsInFlight`，另见 G-X）：两个 effect（`App.tsx` 依赖
     `currentRootId`、`SessionList.tsx` 依赖 `selectedRootId/selectedNodeId`）同一次提交触发时
     只发一次 `/api/pins`。
- 针对性测试：
  - `server/internal/session/compact_toolcall_test.go:TestCompactToolCallEditDropsRedundantMeta`
    — edit 有 content 丢 input/output、无 content 保留、ask_user 保留、execute 只丢 output。
  - `server/internal/session/compact_toolcall_test.go:TestCompactExchangeAuxLightStripsContentForLazyKinds`
    — 轻压缩只清 edit/read/execute 的 content，ask_user/todo 原样保留。
  - `server/internal/session/aux_window_tail_test.go:TestLoadExchangeAuxWindowStripsLazyToolContent`
    — **窗口读路径真的走了轻压缩**（edit content 清空、非冗余 meta 保留），
      且同一条目走全量压缩仍带 content（重锚定靠它）。
  - `web/tests/session-window-dedup.test.mjs` — 并发同参只发一次、结算后能再发、
    `beforeSeq`/`rootId` 不同必须各自发。
  - `web/tests/tool-card-grouping.test.mjs` — 6/5 张成组、4 张不成组、edit+read 分成两组、
    ask_user 永不成组、thought 打断连续段、`"Edit"` 归一化成 `edit`。
  - `web/tests/related-files-debounce.test.mjs` — 去抖表是 ref、键是 `rootID::sessionKey`、
    重复触发先清定时器、请求在 500ms 定时器里发、触发方不再是 `async`。
  - `web/tests/pins-refresh-dedup.test.mjs` — 并发只发一次、结算后能再发、仍走 `controlPath`。
  - `web/tests/session-window.test.mjs` — `SESSION_WINDOW_SIZE = 8` 且被 viewer/App 共用。

### G-AU 会话拉取风暴与渲染主线程阻塞（2026-10-07）

- 来源：`web/src/App.tsx`、`web/src/components/SessionViewer.tsx`、
  `web/tests/session-load-storm.test.mjs`（新增）。
- 边界：**「选中一个拉不到的会话」+「有 agent 在流式输出」同时成立时的那条自持循环**，
  以及**每次渲染里的 O(n²) 与无节流滚动**。合上游时要么全留要么全弃 ——
  这三条单独被冲掉都不会让门禁变红，只有长时间挂着标签页才炸。
- 可见症状（没有它会怎样）：
  1. **界面卡死、标签页崩溃**。实测（`journalctl`，2026-10-06 17:33 → 10-07 08:29，
     持续 18 小时）：`GET /api/sessions/1791268544-ef85840083c5?...` 返回 **404**，
     **22–25 次/秒**，单小时 **5232 次**，累计 **7800+ 次**，且在 `mindfs` 与
     `日程管理` 两个 root 之间交替。浏览器每域只有 6 条连接，这个循环把它们全占满 →
     会话列表 / 文件 / WS 重连全部排队 → 界面卡死 → 标签页被杀 → 自动重载
     （对应访问日志里成对的页面加载 burst）。
  2. **内存持续上涨**：`sessionCacheRef` 无上限（IDB 那份有 500 条 / 200KB 上限），
     长时间浏览堆到几百 MB。
- 机制（两段，缺一不可）：
  1. 加载 effect 的依赖里有 `selectedSessionSnapshot` 这个**对象**，而它的身份随
     `cacheVersion` 每次 bump 都变（`getSessionSnapshot` 的 dep 数组含 `cacheVersion`），
     流式输出每 30ms bump 一次、每个 `tool_call`/`todo_update`/`plan_update` 还同步再
     bump 一次 → effect 每轮都重跑。
  2. `loadedSessionRef` 只在**成功**时置位，404 的会话永远兜不住 → **没有终点**。
- 改了什么：
  - 加载 effect 的依赖改成**身份**（`loadSessionKey` / `loadRootID` /
    `loadSnapshotHasExchanges` 三个原始量），不再依赖快照对象。
  - `restoreActiveSession` 的失败路径记下 `{ at, status }` 并返回 `null`
    （原先异常原样抛出，调用点只写 `.then` 不写 `.catch` → 未捕获拒绝，
    且失败与「还没加载」完全等价）。404 视为**终点**，其余错误退避 5s。
  - 成功 / `markSessionStale` 解禁失败记录（否则「点重试」永远没反应）。
  - `sessionCacheRef` 按条数封顶（64）+ 按最近写入时间 LRU 淘汰。
  - `SessionViewer` 里两处 O(n²)（`timeline.slice(0, idx+1).filter(...)` 与
    `previousUserTimestamp` 反向扫描）改成一次 O(n) 前向扫描 + 查表。
  - 滚动处理用 rAF 合并到每帧一次（原先每个 scroll 事件都跑
    `querySelectorAll` + 逐个 `getBoundingClientRect` + `setState`）。
- 针对性测试：
  - `web/tests/session-load-storm.test.mjs` — 加载 effect 依赖里**不能**出现
    `selectedSessionSnapshot`；失败必须被记录且 404 是终点；成功与 `markSessionStale`
    必须解禁；`sessionCacheRef` 必须有上限且每个新键写入点都要执行；
    两处 O(n²) 必须消失；滚动必须走 rAF。

### G-AV worktree 收尾路径符号链接匹配修复（2026-10-07）

- **症状**：任务卡在「收尾中」，点收尾按钮报「main 正被另一个 worktree 占用
  （/mnt/fnos/...），先把它移开」。实测 `/home/xiaokubao/family` → `/mnt/fnos/family`：
  git 报解析后的路径，`MainCheckoutPath` 对主 checkout 返回调用方给的路径（符号链接本身），
  两者直接字符串比较误判成「两个不同的目录」—— 那个「另一个 worktree」就是主 checkout 自己。
- **根因**：`git worktree list --porcelain` 与 `git rev-parse --path-format=absolute --git-common-dir`
  都会解析符号链接，而 `MainCheckoutPath` 对主 checkout 返回 `filepath.Clean(path)`（不解析）。
  `validateTarget` / `InspectWorktree` / `ListWorktrees` 三处都用 `filepath.Clean` 直接比较，
  在「项目根是符号链接」时全部误判。
- **修复**：新增 `samePath` 辅助函数（先 `EvalSymlinks` 再比较），三处统一使用。
- **为什么必须保留**：`MainCheckoutPath` 的返回值语义不变（对主 checkout 返回调用方给的路径），
  只在比较处统一。符号链接路径匹配是 git worktree 操作的基础，不修则收尾永远卡住。
- **针对性测试**：
  - `server/internal/gitview/gitview_test.go:TestSamePathResolvesSymlinks` — 符号链接路径必须匹配
  - `server/internal/gitview/gitview_test.go:TestValidateTargetDoesNotSelfBlockViaSymlink` —
    通过符号链接访问主 checkout 时 `validateTarget` 不得自我阻塞
  - `server/internal/gitview/gitview_test.go:TestListWorktreesCurrentViaSymlink` —
    根 worktree 的 `Current` 标记在符号链接路径下必须成立

### G-AW 工作台移除 attention bar + 统一 task card wrapper（2026-10-07）

- **症状**：工作台顶部有一堆小的灰色卡片（`WorkspaceAttentionBar`），只显示项目任务阶段，
  下面的任务卡片已经覆盖了它的信息。任务小卡片（`WorkspaceTaskRow`）和看板卡
  （`TaskBoardView` 的 `<article>`）看起来差不多但用了不同的 wrapper 组件。
- **修复**：
  1. 移除 `WorkspaceAttentionBar`（信息冗余：任务卡片已包含状态、阶段、操作按钮）
  2. 将 `WorkspaceTaskRow` 的 wrapper 从 `<div role="button">` 统一为 `<article role="button">`，
     与看板卡结构一致
- **为什么必须保留**：attention bar 是冗余的，移除后工作台更简洁；wrapper 统一后
  两个视图的卡片结构完全一致，维护成本降低。
- **针对性测试**：
  - `web/tests/workspace-board.test.mjs` — attention bar 必须不存在；taskRow wrapper 必须是 `<article>`
  - `web/tests/task-card-wrap.test.mjs` — `workspaceTaskNameStyle` 断言已移除

### G-AX relay 绑定轮询测试的两条同步竞态（2026-10-07）

- 来源：`server/internal/relay/service_test.go`（**纯上游文件**，对 baseline 零差异）、
  `web/tests/relay-bind-poll-sync.test.mjs`（新增）。
- 边界：**`TestManagerPollTerminalBindStatusStopsPolling` 里的两条竞态**。
  合上游时要么全留要么全弃 —— 这两条单独被冲掉都不会让门禁变红，
  只有持续跑或负载高时才炸，而它炸的时候看起来像产品 bug。
- 可见症状（没有它会怎样）：
  1. 该测试偶发失败，报 `pending code did not clear after expired bind status`，
     耗时正好 **5.00s**（撞上 `time.After(5 * time.Second)`）。
  2. 另一条偶发 `expected initial pending code`（`-race -count=20` 实测复现）。
  3. 单独跑 30/30 全绿 —— 所以极易被当成「偶发、不管它」。
- 根因（两条，互相独立）：
  1. **状态落地晚于 channel 发送。** 本文件的 mock transport 在**返回响应之前**
     就把 URL 送进 `requests`（`requests <- req.URL.String()`），而 poller 要等
     响应返回之后才走 `onFinished` 更新 `Status()`（`manager.go` 的 `pollLoop`：
     `m.pendingCode = ""; m.lastError = status`）。原实现从 `requests` 读到请求
     就**立刻**查 `Status()`，不是 `"expired"` 就 `continue` 去读下一个请求 ——
     而 poller 收到 expired 之后已经 `return` 了，再没有下一个请求，于是卡到超时。
  2. **channel 带缓冲（cap 4）导致 poller 抢跑。** 缓冲让发送不阻塞，poller 会
     抢在测试观察之前跑完整个 poll（expired → onFinished → 清空 `PendingCode`），
     于是 `StartBinding()` 返回后立刻查 `PendingCode` 可能是空的。
- 改了什么：
  - 收到请求后**等状态落地**（轮询 `Status()` 直到 `LastError == "expired"`），
    而不是读一次就 `continue`。
  - `requests` 改成**无缓冲**，让 poller 停在发送上，保证测试读到 `PendingCode`
    时它还没被清空。
  - 新增 `TestManagerPollTerminalBindStatusSettlesAfterChannelSend`：mock 在送进
    channel 之后**再睡 150ms** 才返回响应，把第一条竞态从「负载高才偶发」
    变成「必然发生」，并断言「读到请求时状态还没落地」这个前提。
- 针对性测试：
  - `web/tests/relay-bind-poll-sync.test.mjs` — 钉住「不得读一次就 continue」、
    必须有等待循环、`requests` 必须无缓冲、顺序假设必须被确定性复现。
    **已验证：把修复改回原样后该测试立刻变红。**
- 验证：`-count=50` 通过；`-race -count=30` 连跑 3 轮（共 90 次）全绿。

---


### G-AY pending 纯派生：done 丢失时停止符号 /「正在思考」卡住（2026-10-07）

- 来源：`web/src/App.tsx`（`multiProjectPendingByKey` 成为唯一真相；`setMultiProjectSessionPending`
  同步更新 ref 再 setState；三个读点 `getSessionSnapshot` / `resolvePendingForSession` /
  `rootSessionIndicators` 改为派生）、`web/src/app/useRealtimeEvents.ts`（`handleSessionStreamDone`
  不再往会话对象写 pending）、`web/src/app/appSession.ts`（删掉补丁 `clearStalePending`；
  `mergeReplyingStateByNode` 仍是服务端真值的增量合并点）、
  `web/tests/pending-single-source.test.mjs`（新增）。
- 边界：**「这个会话在不在回复」这一份状态的存放方式**。合上游时若退回「会话对象上存 `pending`」，
  就又会裂成多份、又会在丢 `session.done` 时永久卡住 —— 正是本 bug 的可见症状。
- 可见症状（没有它会怎样）：
  1. 任务已结束，输入框仍显示停止符号。
  2. 查看器仍显示「正在思考」，用量 / 上下文窗口面板不出现。
  3. **会话栏是正常的** —— 列表蓝灯每 5s 从 `/api/replying-sessions` 对账一次，
     所以列表收敛、抽屉不收敛，两边看起来「不一样」。
- 根因：
  - `pending` **不落盘**（`sessions` 表无此列，`sessionListResponse` 也不发），纯前端 optimistic。
  - 原先前端有**五份投影**（缓存 / 抽屉 / 选中 / `pendingBySessionRef` / `multiProjectPendingByKey`）。
    只有列表蓝灯那份每 5s 从 `/api/replying-sessions` 对账；其余四份只有 WS `session.done`
    一个出口 —— `BroadcastSessionDone` 只发给 `session.ready` 绑定的客户端，而 WS 连接会反复
    `1006 unexpected EOF` 断连，重连后绑定竞态一丢那条事件，那四处就**永久停在 `true`**。
  - 实测（journalctl）：`1791307447-6f646793904d` 广播了 `session.done`，其后却没有
    `GET /api/sessions?root=…` 重拉 —— 证明该 WS 事件没被前端处理。
- 改了什么（**纯派生，不再存储**）：
  - **砍掉**：`clearStalePending`（第一版的补丁）、对账 effect、`setSelectedPendingByKey`，
    以及所有往会话对象（缓存 / 抽屉 / 选中 / sessions 列表）写 `pending` 字面量的地方。
  - **唯一真相** `multiProjectPendingByKey`：`setMultiProjectSessionPending` 是唯一写入点，
    且**先同步更新 `multiProjectPendingRef` 再 setState** —— 流式期间 `markSessionPending`
    每 chunk 调一次，幂等判断读的是 ref，等 React 跑 updater 就来不及了。
  - **三个读点全部派生**：`getSessionSnapshot` 读 `multiProjectPendingByKey`；
    `resolvePendingForSession` 读同步镜像 ref（不把 state 拉进 `restoreActiveSession` 的依赖）；
    `rootSessionIndicators`（项目尾点）也从表里派生。会话列表项上的 pending 由
    `boardSessionByKey` 用同一张表盖上去（只抹不盖会得到「卡片圆点跑完不灭」）。
  - **`pending-*` 临时键 → 真键**：promote 时把「在跑」这条乐观标记一起搬到真键，
    否则 promote 那一刻就灭灯。
  - 服务端真值仍只由 `refreshMultiProjectReplyingSessions`（5s/visible-only）经
    `mergeReplyingStateByNode` 按节点增量合并写入；`handleSessionStreamDone` 仍负责收尾清键。
  - **没有第二份状态，也就没有「两份不同步」这回事** —— 断连丢事件时，下一个轮询周期
    从服务端真值覆盖即可收敛，不需要为每处存储各写一套对账。
- 针对性测试：
  - `web/tests/pending-single-source.test.mjs` — 源码守卫：唯一写入点收敛在
    `setMultiProjectSessionPending`（ref 先于 setState）；App.tsx / useRealtimeEvents.ts 里
    **零** `pending: true|false` 字面量；三个读点都从 `multiProjectPendingByKey` / ref 派生；
    `handleSessionStreamDone` 必须清唯一真相；轮询 + `mergeReplyingStateByNode` 必须还在。
    **已验证：插入一处 `pending: true` 后该测试立刻变红。**

---

### G-AZ 任务卡视觉件：待审核状态徽标 / 列框去除 / 浮动滚动条 / 工作台角标移除（2026-10-07）

- 来源：`web/src/components/TaskBoardView.tsx`、`web/src/components/FloatingScroll.tsx`（新增）、
  `web/src/components/TaskCardText.tsx`（删除）、`web/src/components/workspace/WorkspaceProjectRow.tsx`、
  `web/src/app/useWorkspaceBoard.ts`、`web/src/i18n/locales/{zh-CN,en-US}.ts`、`web/src/index.css`、
  `web/tests/{task-board-view,task-card-wrap,workspace-board}.test.mjs`、`docs/workspace-design.md`。
- 边界：**任务卡的视觉件** —— 待审核列的状态文字、列框与列表内边距、列内滚动条形态、
  工作台项目头的「需要你」角标。这四件共享同一组渲染文件，合上游时要么全留要么全弃。
- 可见症状（没有它会怎样）：
  1. **看板「待审核」列的卡片没有任何状态指示**（用户 2026-10-07 实测）：标题行只有
     编号 + 名字 + worktree 标签，看不出这张卡在等用户回话。工作台卡同一张卡显示 `· 待审核`
     （棕黄 `var(--status-warn)`），两边信息量不一致。
  2. **列框把卡片挤窄**：section 的 `border` 占 2px、列表 `padding: 8px` 又占 16px，
     卡片比工作台窄 18px。去掉框 + padding 降 6px 后每张卡宽约 6px。
  3. **原生滚动条占 6px 宽**：全局 `::-webkit-scrollbar { width: 6px }` 把每列卡片再挤窄 6px。
  4. **工作台项目头显示「需要你3 3」**：`blockedCount` 角标和总数角标在「组内任务全部待审核」
     时显示同一个数字，用户实测为重复内容。
- 为什么必须保留：
  - 状态文字走 `TaskCardRows` 现有 `showStatus` 路径（`showStatus` prop），与工作台卡
    **同一渲染路径** —— 不是另开一套。上游若改 `showStatus` 的渲染位置，两边一起变，不会分叉。
  - 列框去除后列的边界靠列头 `borderBottom` 分隔线 + 网格 `gap: 6px` 表达，不依赖底色。
  - `FloatingScroll` 自绘浮动拇指（overlay 语义：悬停/滚动可见，停手 1.2s 淡出），
    替代 `overflow: overlay`（已废弃、Firefox 不支持）。
  - 「需要你」角标移除后，待审核信息由卡标题行的状态文字 + 「待审核」筛选档位承载，不丢。
- 针对性测试：
  - `web/tests/task-board-view.test.mjs` — 钉住「待审核列 `showTaskStatus` 为真」、
    「列框四件（border+圆角+浅灰底+overflow）整组消失」、「列表容器是 `FloatingScroll`
    且 padding 归零（G-BB 进一步从 6px 降到 0）」、「index.css 有 `.mindfs-floating-scroll` webkit 隐藏规则」。
  - `web/tests/task-card-wrap.test.mjs` — 钉住「看板与工作台都渲染 `TaskCardRows`」、
    「工作台卡不传 children（无正文）」、「卡面都来自 `taskCardSurfaceStyle`」、
    「`TaskCardText.tsx` 死代码已删除」。
  - `web/tests/workspace-board.test.mjs` — 钉住「`task.workspaceAttention` 不在 locale」、
    「项目头不渲染该角标」、「hook 源码不再出现 `blockedCount`」。


---

### G-BA 前端目录按领域重组 + 服务/组件文件拆分（2026-10-07）


- 来源：`a83950b`（source-map 统一读层）、`f5c4e0d`（components/services/shared 按域重组）、
  `7b6d969`（services/{task,git,file} 拆成目录模块）、`def8b7e`（FileTree 图标子组件抽出）。
- 边界：**前端目录结构与文件拆分**。`components/` 下 40+ 个 `.tsx` 平铺 →
  `components/{session,task,file,git,root,agent,account,shell,common}`；
  `services/` 下 20+ 个 `.ts` 平铺 → `services/{net,platform,prefs,task,git,file}`；
  纯键工具 → `shared/`。`services/{task,git,file}.ts` 各拆成目录模块，
  原路径保留 barrel shim（`export * from "./x/index"`）。
  **拆分策略是「机械搬移 + 旧路径 barrel 重导出，零行为变化」**：对外导出面不变，
  只搬文件。43 条源码正则断言（读旧逻辑路径）由 `web/tests/source-map.mjs`
  统一读层兜住（逻辑路径 → 物理文件列表，`readSource` 拼接）。
- 可见症状（没有它会怎样）：上游把文件放回扁平结构（`components/*.tsx`、`services/*.ts`
  平铺）时，本地拆出的分域目录与 barrel shim 被整份覆盖 —— 43 条源码正则断言与
  barrel 重导出同时失效，会话/任务/文件/git 四域的模块边界消失，导入环与
  「同一判定两处实现」这类重复悄悄回来（文件级 diff 看不出来）。
- 理由：前端原为扁平巨文件结构，单文件最大 10472 行（`App.tsx`）。按域重组后
  模块边界 = 职责边界，是后续 App.tsx 拆分的地基。
- 验证：`web/tests/frontend-split.test.mjs`（barrel 完整重导出 + 无导入环 +
  source-map 映射）、`web/tests/source-map.test.mjs`（读层完整性）。
- 已知边界：`session.ts` 暂不拆分 —— 其 VM 执行类测试（`session-window` 等）依赖
  单文件自包含，拆分会破坏沙箱 `require`，已回滚。
---

### G-BB 看板列头灰底 + 间距对齐工作台（2026-10-07）

- 来源：`web/src/components/task/TaskBoardView.tsx`、`web/src/components/file/DefaultListView.tsx`、
  `web/tests/task-board-view.test.mjs`。
- 边界：**看板列头视觉与间距** —— 列头灰底、列表 padding、外层 padding。
  这三件共享看板的布局与视觉，合上游时要么全留要么全弃。
- 可见症状（没有它会怎样）：
  1. **列头与卡片内容视觉混淆**：列头没有灰底，与卡片内容混在一起，
     用户难以区分「栏目标签」和「卡片内容」。
  2. **卡片比工作台窄 12px**：列表 padding 6px 每侧，卡片被挤窄。
     去掉后卡片与工作台等宽。
  3. **看板边缘间距比工作台大**：外层 padding `8px 16px 24px` vs 工作台 `6px 8px`，
     看板内容区比工作台窄，视觉上不对齐。
  4. **列头灰底直角贴卡片**：灰底直角与第一个任务卡直接相连，
     视觉上过于拥挤；圆角 + 下方 6px 空隙让列头与卡片有呼吸感。
- 为什么必须保留：
  - 列头灰底 `rgba(148, 163, 184, 0.08)` 与模板筛选按钮的底色一致，
    是看板视觉语言的一部分。
  - 列表 padding 归零后，卡片宽度 = 列宽，与工作台卡片等宽。
  - 外层 padding `6px 8px` 对齐工作台容器，视觉上两端一致。
  - 列头圆角 `borderRadius: 6px` + 下方 `marginBottom: 6px` 让列头灰底
    与任务卡之间有空隙，视觉上更通透。
- 针对性测试：
  - `web/tests/task-board-view.test.mjs` — 钉住「列头有灰底 `rgba(148,163,184,0.08)`」、
    「列表 `FloatingScroll` padding 归零」、「外层 padding `6px 8px` 对齐工作台」、
    「列头 `borderRadius: 6px` 圆角 + `marginBottom: 6px` 下方空隙」。

---

### G-AY pending 信号兜底（2026-10-08 建，同日修订：删掉第②③层）

- 来源：`server/internal/api/stream_hub.go`、`server/internal/api/appcontext.go`、
  `web/src/App.tsx`、`web/tests/e2e-pending-signal.test.mjs`。
- 边界：**「会话完成后 pending 信号必须消失」这一行为的实现方式**。
- 可见症状（没有它会怎样）：
  对话完成后仍显示「正在生成」+ 停止键，用户以为还在跑，实际早已完成。
  根因：`BroadcastSessionDone` 中 `ClearSessionPending` 自旋无超时，replay 客户端排不空时
  卡住，`session.done` 广播永远发不出去，前端 pending 永远 true。
- 为什么必须保留：
  只剩第①层（必修）：服务端 `ClearSessionPending` 加 2 秒超时 + 广播挪到清之前。
  合上游时若退回「`ClearSessionPending` 无超时自旋」，就又会卡住。
- **第②③层已于 2026-10-08 删除**（同日引入同日删）：
  - 第②层（服务端 `LastEventAt` 字段 + `PendingSessionSnapshot` 30 秒超时判据）是
    **死代码** —— `Active` 字段没有任何消费方（唯一消费者 `notifySessionDone` 不读它），
    删前删后行为完全一致。
  - 第③层（前端 `lastStreamEventAtRef` + 轮询连续 3 周期 15 秒无事件则本地降级）是
    **真 bug**：真在跑的会话会长时间没有 stream 事件（服务端不转发 SDK 的 `keep_alive`
    心跳，唯一的周期事件 `ToolProgressMessage` 只覆盖 tool 执行，思考 / tool 间隙 / 起始
    间隙都没有事件），按静默判「已结束」会「明明在跑却灭灯」、亮灯时长随事件节奏漂移。
  - 第③层想兜的「WS 断连」场景，**5s 轮询已经兜住**（服务端 `ClearSessionPending` 在
    回合结束时删条目，下一轮轮询就灭灯），所以它纯属多余且有害。
- 针对性测试：
  - `web/tests/e2e-pending-signal.test.mjs` — 钉住「agent 行落盘后 30s 内 pending 信号消失」、
    「WS 断连 20s 后 pending 信号收敛」（后者靠轮询，不靠静默降级）。
  - `web/tests/pending-single-source.test.mjs` — 钉住「pending 纯派生自 `multiProjectPendingByKey`」。

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


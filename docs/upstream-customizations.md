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
> 本文档 §1-§3 分组为 v0.4.7→v0.4.9 时代沉淀，机制仍适用；v0.5.1 / 7757ca8 合并记录见 §0，v0.5.5 见 §0-B，接回 main 见 §0-B-2 / §0-C。
>
> **fork 与上游的关系是两条线，别混**：`upstream/main`（tag `v0.5.5` = `9ab9572`）是 **a9gent 上游**，
> 它的定制清单就是本文件；`origin/main` 是本 fork 自己的主线，**近期全部在 task-35 上**（2026-10-11
> 第三次分布合并把 main 的 `44f1497..37a0a1f` 全吸收进来，见 §0-C）。所以「接回 main」指的是 fork 的 main，
> 与 `make check-upstream` 算的 delta（对上游 `9ab9572`）是两件事 —— 门禁算的是「fork 相对上游改了什么」，
> 永远以 `9ab9572` 为基准，不会因为 fork 内合并而变。
>
> 历史口径（2026-08-26 首次成文时的记录，**仅作沿革参考，不再是核对基准**）: 基准 `upstream/main` tag `v0.4.7` `18b10ca`，
> 对比 `HEAD = 0591238`，`git log --reverse 18b10ca..HEAD` 44 提交，`git diff 18b10ca...HEAD` 110 文件 `+8333/-4249`。
> 分组原则: **按 hunk 归类**——同一提交、同一文件不同行可归不同组；每行改动仅属一组，组间互斥、全体完备。
> 判定: `git show --numstat/--stat` 逐提交核验 + 逐 hunk 归类，`git cherry -v` 校验上游等价。

### G-BD. 移动端恢复后的 pending 对账与服务端事件时间

- **可见症状**：手机切到后台导致 WebSocket 断开并丢失 `session.done` 后，回复灯可能一直亮到下次偶然刷新；页面恢复后回复时长会把恢复时刻当成服务端事件时刻，显示出虚假的长耗时。
- **根因与边界**：页面恢复必须主动触发 `/api/replying-sessions` 对账；流事件的时间由服务端写入，客户端 replay 优先使用该时间，旧事件才回退到接收时刻。多节点分组以分组自身 `_nodeId` 构造 pending key，避免同名项目跨节点串灯。
  **重放路径必须经 `cloneEvent` 保留 `Timestamp`** —— 手机恢复走的是重放而不是 live，漏掉它客户端只能回退到「恢复页面的接收时刻」。服务端 `time.Time` 零值会序列化成 `0001-01-01T00:00:00Z`（struct 的 `omitempty` 不生效），客户端必须把这类哨兵当成「没有时间戳」，否则时长显示成天文数字。
- **针对性测试**：
  - `web/tests/pending-recovery-on-resume.test.mjs` → `visibilitychange`/`pageshow` 恢复时触发 pending reconciliation。
  - `web/tests/cross-node-replying-state.test.mjs` → 同名项目使用实际节点作用域投影，失败节点保留旧状态。
  - `web/tests/session-duration.test.mjs` → replay 使用服务端事件时间、零值哨兵回退，不用恢复页面接收时间。
  - `server/internal/api/ws_test.go` 的 `TestReplayPreservesEventTimestamp` → 重放事件保留服务端时间戳（`cloneEvent` 不得丢字段）。



- **Merge commit `976a5d0`**（双 parent：上游 `9ab9572` × 本地 `bb4244d`），merge-base `v0.5.2`(`7757ca8`) → **`bb4244d`**。
  上游 25 commits / 125 files，本地 311 commits / 304 files，重叠 39 files；合并相对本地 +5400 / −488（95 files）。
- 12 个前端冲突文件 / 51 段，**逐 hunk 定解，无 `--theirs` 整文件**（重文件铁律）：
  `App.tsx` 25 · `ActionBar.tsx` 11 · `SessionViewer.tsx` 3 · `services/tasks.ts` 2 · `SessionList.tsx` 2 ·
  `useSessionStream.ts` / `TaskTemplateDialog.tsx` / `FileViewer.tsx` / `DefaultListView.tsx` /
  `CodexRateLimitIndicator.tsx` / `i18n en-US` / `i18n zh-CN` 各 1。
- **架构级取舍：拒绝上游 task_groups 编排体系**（用户决策）。上游给看板加了 `task_groups` 分组 + 并发调度器 +
  `scheduler_admitted` 准入位；本地早已按 AGENTS.md 事实 13 重做为「卡=任务、列=全局状态、无并发调度」。
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

### 0-C. 接回 main：`44f1497` → `d027981` + `37a0a1f`（2026-10-11，第三次分布合并）

- **两次 merge**（用户决策：分布合并，本分支直接合）：
  - `67d8f25`（`fb3c1c7` × `d027981`）吸收 6 个提交 —— G-BG 会话窗口读按需读 / tail 增量三处缺陷 /
    搜索 cap，G-BH S2a 双写 + S2b 读改走块流 + C1/C2 轮次身份，S2c 身份判重，S2d-1 旧数据块流回填。
  - `8ff2e93`（`67d8f25` × `37a0a1f`）吸收 S2d-2「块流成为正文唯一载体，主日志 content 恒空」。
    **合并期间 main 又前进一个提交**，第二次 merge 是赶上的；`merge-tree --write-tree` 干跑预判**零冲突**。
- **冲突化解原则：main 的单体形态不回流，一律落到本分支拆分后的归属文件**（用户明确要求
  「应该把所有的修改都挂入正确的位置 保持 app tsx 干净」）。四个冲突文件逐一：
  - `services/session.ts`：保留根路径纯 facade，SessionService 实现在
    `services/session/sessionService.ts`，**未**回退 main 的 2741 行单体。blocks 类型落在
    `sessionTypes.ts`、清洗判据 `toPersistentBlocks` 落在 `sessionExchangeMerge.ts`（「seq>0」
    这把尺仍只有一个实现文件，见 message-mechanisms 冲突①②）。
  - `components/session/SessionViewer.tsx`：保留 651 行，块流窗口态落在 `useSessionWindowing.ts` /
    `hooks/sessionBlocks.ts`。
  - `hooks/useSessionStream.ts`：2 行差异即 `dispatcher` 拆分（`sessionService.dispatcher.isSessionStreaming`），
    非定制，是拆分后的正确形态。
  - `app/useSessionLifecycle.ts`：承接 main 在 App.tsx 里的 +8 行缓存窗口 blocks 合并 —— **按 seq 覆盖，
    不拼接**（同一 seq 的块集合是完整快照，拼接会让旧新 revision 并存渲染成两段重复正文）。
- Go 侧定制（`session/blocks.go`、`manager.go`、`types.go`、`usecase/session_blocks_backfill.go` 等）**无冲突**，
  原样吸收；两端新增 20 个 Go 测试文件全部并入。
- **测试侧为拆分改了 3 处读法**（不是产品代码的问题）：`message-mechanism-merges.test.mjs` 需按
  **逻辑路径**读拼接视图（App.tsx 已拆）与按**物理文件**读（「全仓只允许一个文件实现这把尺」这类断言，
  否则同一处命中两次）两种读法，为此在 `source-map.mjs` 导出未被 patch 的 `rawReadFileSync`；
  `session-window.test.mjs` 的 vm require 桩要同时能裸调（`generateClientId`）与 `new`
  （`SessionConnectionManager`），两个探针改为真实 import 拆分后的实现。
- **本次一并修掉门禁的历史债**：`make check-upstream` 在合并前（`fb3c1c7`）就是红的 ——
  2 个 delta 文件未登记 + 11 处锚点失配，全部源于前三轮拆分后锚点坐标过期（符号还在，搬了文件）。
  逐条 `grep -rn` 定位真实位置后改锚点路径，无一条是定制丢失；`clearStalePending` 是唯一例外
  （被 G-AY 纯派生改造自己废除，删锚点而非搬）。门禁与测试补登记办法已写进 §7 附录维护约定。
- 门禁全绿：`go test -count=1 ./...`（25 包 ok）、`tsc --noEmit` 0 err、web **223 用例 / 0 fail**、
  `make check-upstream` **837 delta 文件被 57 组完整覆盖**、App.tsx 16 行、全仓冲突标记扫描为空。
  定制清单与 main **零组丢失**（yaml 57 id 双向无差异）。**本次清理**：`G-AY` 原先被两组共用（pending 纯派生 / pending 信号兜底），而门禁不校验 id 唯一性，`git log --grep 'Scope: G-AY'` 会同时命中两组无从区分 —— 后者改名 `G-AX`（取空闲号），md/yaml/正文引用一并改。
- 部署：**未推送、未部署**（本机 `systemctl restart mindfs` 由用户执行），
  待用户确认后走 `ship` skill。

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

## 1. 总览（92 组，组 id 以 yaml 为准）

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
| G-AB | 会话投影与审计退役 | 裁剪 | 见 §3.1 | `session_projection` / `audit` |
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
| G-AR | 移动端侧栏切换按钮移入中间主栏共用 header | 修复 | 见 §3.1 | 按钮插在中间主栏 36px header 两侧（不额外占行），仅移动端；侧栏自身不显示；文件/插件预览也必须用 ViewHeader |
| G-AS | ACP 提问（dsh `ask_user_question` 走 elicitation） | 修复 | 见 §3.1 | 只对 dsh 广告 elicitation.form；题目 id+文本关联；答案编码成 `question_<i>` |
| G-AT | 会话打开性能（窗口去重 / 载荷压缩 / 工具卡分组） | 性能 | 见 §3.1 | 会话打开热路径：取窗去重 + 窗口轻压缩 + 窗口 8 + 工具卡分组 + related-files 去抖 |
| G-AU | 会话拉取风暴与渲染主线程阻塞 | 修复 | 见 §3.1 | 加载 effect 只依赖身份不依赖快照对象；失败留痕且 404 是终点；`sessionCacheRef` 有上限；渲染无 O(n²)、滚动有节流 |
| G-AY | pending 纯派生（done 丢失时停止符号/「正在思考」卡住） | 修复 | 见 §3.1 | 原先「在不在回复」在前端有五份投影，只有列表蓝灯那份每 5s 对账；其余四份只有 WS `session.done` 一个出口，断连/重绑竞态丢了那条事件就**永久卡在 true**。2026-10-07 改成纯派生：唯一真相 `multiProjectPendingByKey`，会话对象上不再存 pending |
| G-AZ | 任务卡视觉件（待审核徽标/列框去除/浮动滚动条/工作台角标移除） | 视觉 | 见 §3.1 | 待审核列补状态文字（与工作台同路径）；列框去除+padding 归零卡片加宽 18px；FloatingScroll 浮动滚动条不占宽；工作台「需要你 N」角标移除 |
| G-BB | 看板列头灰底 + 间距对齐工作台（列头背景/列表 padding/外层 padding） | 视觉 | 见 §3.1 | 列头灰底区分栏目标签与卡片；列表 padding 归零卡片加宽 12px；外层 padding 6px 8px 对齐工作台容器 |
| G-AV | worktree 收尾路径符号链接匹配修复 | 修复 | 见 §3.1 | worktree 路径经符号链接时前缀匹配失效，收尾键被误判为「目录不存在」 |
| G-AW | 工作台移除 attention bar + 统一 task card wrapper | 重构 | 见 §3.1 | 列表与工作台共用同一个卡片外壳 |
| G-AY② | 抽屉 pending 对账：done 丢失时停止符号/「正在思考」卡住 | 修复 | 见 §3.1 | 纯派生的最后一处投影（抽屉会话）也要收敛到同一真相 |
| G-AX | pending 信号兜底（`ClearSessionPending` 2s 超时 + 广播先于清） | 修复 | 见 §3.1 | 自旋无超时会卡住广播，`session.done` 永远发不出去。**注：本题根因经 2026-10-09 复核为误判，真正成因见 G-BE**。原与纯派生共用 `G-AY` id，2026-10-11 改名 |
| G-BC | 看板/工作台卡片统一 + worker 账户 id 从用户名派生 | 修复 | 见 §3.1 | 投影补齐 + 收尾键门控；worker 没有账户表，账户 id 由用户名推导（方案 B） |
| G-BA | 前端目录按领域重组 + 服务/组件文件拆分 | 重构 | 见 §3.1 | `services/session.ts` 2741 行 → 501 行 facade + 19 个兄弟模块；`account/action/file/git/session/task` 按域分目录 |
| G-BD | 移动端恢复后的 pending 对账与服务端事件时间 | 修复 | 见 §3.1 | `visibilitychange`/`pageshow` 必须主动对账；replay 必须保留服务端 `Timestamp`（零值哨兵要当「没有时间戳」） |
| G-BE | pending 的回合身份与单一终结器（迟到 done 不误抹新轮 + 终态边必达） | 修复 | 见 §3.1 | pending 条目没有回合身份，迟到 `done` 会误抹新开的轮次；终态边必须必达 |
| G-BF | Full 同步丢最新几轮：ctx_seq 切片恒空 + 时间地板被跳过 | 修复 | 见 §3.1 | 切片上界写错导致恒取空；时间地板在不该跳过的分支被跳过 |
| G-BG | 会话窗口读不整表解析 + tail 增量三处缺陷 + 搜索 cap | 修复 | 见 §3.1 | 窗口读改按需读；tail 增量三处边界；搜索加 cap；两条全量 aux 热路径改按需 |
| G-BH | 会话正文块流（块存储层 + 轮次身份 + 双写 + 读改走块流 + S2d-2 主日志不再存正文） | 修复 | 见 §3.1 | **本分支与上游 main 的共同前线**（2026-10-11 合并吸收）：空正文轮次落库成 `content:""` 是根因；轮次靠 `turn_id` 有权威身份；S2d-2 起主日志 `content` 恒空、块流是唯一载体 |
| G-BI | 会话身份收敛为两套 id（mindfs_id + agent_id）及 UI 身份对账 | 重构 | 见 §3.2 | UUIDv5 确定性 mint；pending 复用落盘行身份；ACP 回显不冒充 agent_id；缓存按角色类别 + mindfs_id 对账 |
| G-BJ | 会话列表按实时活动时间排序 | 修复 | 见 §3.2 | 置顶/普通各自按最新活动倒序；流块推进临时 activity_at，服务端时间追上后收敛 |
| G-BK | 会话模型选择与运行态一致性 | 修复 | `f7cc9af` | 发送前持久化选择模型；活跃会话模型以 runtime 为准 |
| G-BL | 看板新建任务的 worktree 节点路由 | 修复 | 见 §3.2 | worktree 能力、分支查询与任务创建必须针对同一 nodeId::rootId |
| G-CM | TaskBoardView 契约与任务呈现资源归位 | 架构 | 见 §3.2 | shared contracts + task presenter；app 旧路径只留兼容 facade |
| G-CN | 前端 Vitest 运行链与复用件覆盖 | 测试 | 见 §3.2 | 新测试走 Vitest；旧 Node 源码守卫继续保留 preload hook |
| G-CO | 前端 task view 硬线拆分 | 架构 | 见 §3.2 | scheduled-task 列表与卡片收尾按钮保持独立组件及原交互 |
| G-CP | ProjectAdd 模式选择面板提取 | 架构 | 见 §3.2 | 三个模式入口的标签和选择回调继续由独立面板呈现 |
| G-CQ | AgentSelector trigger 展示提取 | 架构 | 见 §3.2 | 触发按钮视觉保持，菜单状态变更继续归父 hook/controller |
| G-CR | TaskInline 候选菜单呈现提取 | 架构 | 见 §3.2 | 候选列表选择语义保留，查询与编辑状态继续归父组件 |
| G-CS | Login 更新说明折叠区提取 | 架构 | 见 §3.2 | 展开状态和更新逻辑继续由登录控制器管理 |
| G-CT | Git 状态分支选择器提取 | 架构 | 见 §3.2 | 分支获取由现有 hook 承担，开关与切换状态归父面板 |
| G-CU | DocumentViewer PDF 预览职责提取 | 架构 | 见 §3.2 | PDF canvas 有界渲染与跨格式错误语义保留 |
| G-CV | SessionCard 溢出操作菜单提取 | 架构 | 见 §3.2 | 操作和确认继续通过既有父层回调，不改会话状态归属 |
| G-CW | DefaultListView 排序菜单提取 | 架构 | 见 §3.2 | 排序值与弹层状态仍由目录视图控制器持有 |
| G-CX | AgentConfig lifecycle 与样式拆分 | 架构 | 见 §3.2 | FileTree 继续使用旧 facade，生命周期动作归原 hooks |
| G-CY | AgentConfig agent 选择步骤提取 | 架构 | 见 §3.2 | 表单流程状态与 restart/choose 动作仍经父 facade 管理 |

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
| `05726f4` | `docs/message-mechanisms.md` | 消息收发/渲染机制的清点与整合分析（112 个机制、14 条冲突、ADR）。**2026-10-10 更新**：原 `docs/message-mechanisms-plan.md`（G-T 线在 `2328f0e` 上的冻结计划，其代码未采用进 main —— canonical adapter / FIFO writer 留在 `canonical-turn-wip` 分支）**已删除**，计划部分由 `docs/session-blocks-plan.md` 接手；本文件只保留机制清点与冲突清单 |
| 2026-10-13 | `.claude/rules/`（25 文件）+ `web/tests/naming-convention.test.mjs` | 前后端开发规范（长期规则 + TODO 分家、reviewer 清单），及命名守卫测试 |

> `.claude/rules/` 是 fork 的工程约定（上游没有）。合上游后若整目录被删，规范与「哪条该留」的判据一并消失；`naming-convention.test.mjs` 是命名规则的可执行守卫（`.ts` 无 JSX、文件名=主 export 名、`Utils` 纯函数）。


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
| `8e7d857` / `adf610a` | `web/src/App.tsx`、`web/src/services/session.ts` | 保留单项目列表缓存；退役多项目列表的 IndexedDB 预显示，避免打开时先显示旧快照、收到节点数据后再换序 |

> `df445bd` 末尾的 `context_window` 徽标继承与 `session.done→replace` 实为 G-G 语义，但实现落在同提交的 `App.tsx` 同一 hunk，按「主因」归 G-F，评估中单列说明。

### G-G 会话生命周期与 WS 正确性

| 提交 | 文件 | 说明 |
|------|------|------|
| `bd52027` | `web/src/hooks/useSessionStream.ts:3行` | `message_done` 立即清流式标记，修“正在生成”卡住 |
| `8bc5a43` | `server/internal/agent/claude/session.go:Interrupt 3s 超时` + `server/internal/api/ws.go:go handleCancel` | 单读 goroutine 异步派发 + 中断超时，修停止按钮失效 |
| 2026-10-13 | `server/internal/agent/acp/process.go:CancelCurrentTurn` + `server/internal/agent/cancel_rpc_timeout_guard_test.go` | 补齐 acp 取消路径的 5s 超时（原为裸 `Background()`）；加源码守卫扫全部 Cancel/Interrupt 函数体，跨进程 RPC 必须带 `WithTimeout` |
| `7f8a8c6` | `web/src/App.tsx` | 会话历史同步滞后（`loadSessions` 时序） |
| `9df43e9` | `web/src/App.tsx:seq 守卫+pending draft` | `loadMultiProjectSessionGroups` 竞态守卫、pending→真实会话替换、多项目实时更新 |
| `af0a37f` | `server/internal/api/stream_hub.go/ws.go` 辅助 hunk | `model_display_name` 重放与 `session.accepted` 乐观缓存（与 G-R 的探测 hunk 分离） |

### G-H 旧功能裁剪

| 提交 | 涉及文件 | 说明 |
|------|----------|------|
| `df732af` | `web/src/components/RelayLocalServicesDialog.tsx`(删)、`web/src/services/relayServices.ts/tokenStation.ts/launcherNodeSync.ts/nativeBridge.ts`、`web/src/App.tsx/FileTree.tsx/Login.tsx/base.ts/bootstrap.ts/i18n` | 移除公网访问/Relay 对话框/Token 加油站三件套 |
| `20f2b90` | `.gitignore` | `AGENTS.md` 忽略（配置类，不影响运行时，单列但归本组） |
| 2026-10-08 | **删包** `server/internal/relay/`（8 文件）、`server/internal/e2ee/`（4 文件）；**删路由** `server/internal/api/http_relay_services.go`、`http_token_station.go`、`http_local_cli_relay_test.go`；**删前端** `web/src/services/e2ee.ts`、`RelayLocalServicesDialog.tsx`、`SessionQuickActions.tsx`、`relayServices.ts`、`tokenStation.ts`；**清接线** `server/internal/api/http.go`(-549)、`ws.go`(-73)、`appcontext.go`、`agent/config.go`、`nodeinfo/role.go`（去掉 `/api/relay`、`/api/e2ee` 两条前缀）、`cli/cmd/mindfs.go`(-193)、`server/app/server.go`(-148)、`web/src/services/net/{bootstrap,api}.ts`、`session.ts` | **彻底删除 relay 隧道与 e2ee**（用户 2026-10-08 明确要求）：不是「关掉开关」而是「不存在」—— 删包、删路由、删所有前端入口与调用点、删 e2ee proof/解密与 `e2ee_*` 头 |
| 2026-10-09 | `server/internal/update/service.go`(-23)、`server/internal/update/service_test.go`、`web/src/services/appUpdate.ts`、`web/src/i18n/locales/{zh-CN,en-US}.ts`、`web/tests/upstream-removals.test.mjs` | **断掉上游 relay 域名依赖 + 清死文案键**（用户 2026-10-09 拍板「一起砍掉」）：删 `relayDownloadBase`/`relayAssetURL` 与下载回退（`downloadReleaseAsset` 只认 GitHub 给的 asset 地址）；前端删 `DEFAULT_ANDROID/HARMONY_VERSION_URL`、`RelayVersionResponse` 改名 `AppVersionResponse`，版本检查地址缺省为空（只由 `VITE_*_VERSION_URL` 显式提供，未配置即按「无更新信息」返回、不拿空 URL 去 fetch）；删两个已无代码引用的死键 `fileTree.relayLocalServices` |
| 2026-10-09（二） | `internal/deploy/prefix.go`、`internal/deploy/prefix_test.go`、`server/internal/api/prefix.go`、`server/internal/api/http_test.go`、`web/src/services/net/prefix.ts`、`web/src/main.tsx`、`web/vite.config.ts`、`web/tests/deploy-prefix.test.mjs` | **删掉 relay 时代的资源别名前缀**（用户拍板「现在砍掉」）：服务端 `deploy.RelayAssetsAlias()`、`StripDeployPrefix` 里的 `/<前缀>-assets/` → `/assets/` 映射分支；前端 `RELAY_ASSETS_PREFIX` 导出与 `main.tsx` 的资源判定分支；`vite.config.ts` 里注入 SW 的 `RELAY_ALIAS`、fetch 时对它的跳过分支、`mainAssetReSource` 里的别名备选，以及 SW 里把 relay 的 `/n/<token>/` 路径归一化掉的 `normalizedPathname`。**判据**：产生这些 URL 的两处（服务端 relayed 绝对化改写、relay 的 `/n/<token>/` 路径方案）已随 2026-10-08 那批删除，构建产物里恒为 `<部署前缀>/assets/index-*`；**留一半比全留更糟**——服务端删了、SW 还认，排查者会以为别名仍受支持 |
| 2026-10-09（三） | `harmony/entry/src/main/ets/pages/Index.ets`（纯删除 237 行） | **删掉 Harmony 原生壳里的 relay 分支**（用户拍板「纯删除」）：6 处「当前页面是不是上游 relay 页面」的判定（`shouldSyncRelayNodes`／`shouldPatchRelayNodeShell`（本就无人调用）／`shouldPatchRelayListSafeArea`／`replyPollerBaseUrlFromPageUrl` 的 `/n/<token>/` 分支）＋它们驱动的注入脚本（relay `/nodes` 抓节点列表的 `injectRelayNodeSync`、列表安全区补丁 `injectRelayListSafeArea`、启动器里藏「mindfs relayer」按钮的 `injectLauncherRelayEntryVisibility`）＋ `MindFSLauncherNodeSync` 这条 JS 桥（注册／反注册／页面构建三处）。**判据**：这些判定不发起任何外连，只在处于 relay 页面时才生效，属被动死分支；而服务它们的 web 半边 `web/src/services/launcherNodeSync.ts` 已随 2026-10-08 那批删除。**注意**：本仓库没有 ArkTS 构建环境，改动**无法编译验证**，只留了文本级复活检测 |

- 保留判据：**用户明确要求完整删除，没有「保留一部分」的中间态**。
  合上游时上游会把它们给回来，每次都要重新删一遍 —— 删漏就会留下「点了报错」的残缺入口，
  或让 `nodeinfo` 的控制面前缀表多出 `/api/relay`、`/api/e2ee` 两条永远没有 handler 的死路径。
- 本组同时**吸收**了原 `G-AX`（relay 绑定轮询测试的两条同步竞态，2026-10-07）：
  那两条修复住在 `server/internal/relay/service_test.go` 里，包已删，条目随之退役。
- 针对性测试：`web/tests/upstream-removals.test.mjs` —— 钉住「被删文件不复活」+
  「没有悬空 import 指回已删模块」+「`appUpdate.ts`／`update/service.go` 里不得再出现
  上游 relay 域名」+「两个 i18n 里不得再有 `fileTree.relayLocalServices` 死键」；
  `server/internal/update/service_test.go:TestDownloadReleaseAssetDoesNotFallBackToRelay`
  —— 下载失败只允许请求主地址一次、错误里不得出现 relay。
  **两条都已做变异验证**：把下载回退注入回去、把死键灌回 zh-CN，分别变红。
  第二批（资源别名）同样进了 `upstream-removals.test.mjs`：`internal/deploy/prefix.go`、
  `server/internal/api/prefix.go`、`web/src/services/net/prefix.ts`、`web/src/main.tsx`、
  `web/vite.config.ts`、`web/index.html` 里不得再出现 `RelayAssetsAlias`／`RELAY_ASSETS_PREFIX`／
  `RELAY_ALIAS`／`MINDFS_MAIN_ASSET_RE`／`isRelayNodePage`；
  `deploy-prefix.test.mjs` 的断言从「别名必须存在」翻成「别名与主包兜底都不得存在」。
  第三批（Harmony 壳）只有**文本级**复活检测（`harmony/.../Index.ets` 里不得再出现
  `relay.a9gent.com` 与 `MindFSLauncherNodeSync`）—— 本仓库没有 ArkTS 构建环境，
  native 侧改动无法编译验证，这一点必须在下一次真跑 Harmony 构建时人工确认。
- **仍在引用上游 relay 域名的位置（2026-10-09 盘点，尚未处理）**：`scripts/install.sh`、
  `scripts/install.ps1`（一键安装脚本从这里取二进制）、
  `README.md`/`README.zh.md`（文档里的安装地址）。这几处的取舍各不相同
  （安装脚本没有替代下载源就不能直接删；README 只是文案），等用户逐个定夺；
  先在此留痕，免得下次盘点又从头找一遍。
  （原表里的 `harmony/.../Index.ets` 已于 2026-10-09（三）处理，故从此表移除。）
- **Harmony 侧尚未处理的相邻死代码（2026-10-09 盘点）**：`nativeBridgeMethodList()` 里的
  `consumeRelayNodes`／`getLauncherNodes`／`setLauncherNodes`／`storeRelayNodes`，以及
  `hydrateLauncherNodesFromNative`／`injectLauncherNodesFromNative` —— 它们是「启动器
  从 native 取节点列表」那套东西的 native 半边，web 半边
  `web/src/services/launcherNodeSync.ts` 已随 2026-10-08 删除。**没有一并删**是因为它属于
  「删一整个功能」而不是「删 relay 判定」，且同样无法编译验证；等真跑一次 Harmony 构建
  时一起处理，避免在无法验证的 native 代码上叠两次改动。

### G-I 安全模型重塑（API 层不鉴权 / CORS 全开放）

| 来源与 hunk | 文件 | 说明 |
|-------------|------|------|
| `c7c2c45:http.go#protectedEndpoint` | `server/internal/api/http.go` | 直通（`ponytail: 鉴权已移除`）—— 上游的 token / e2ee proof 中间件不再包裹 `/api/*` |
| `c7c2c45:http.go#corsMiddleware` | 同文件 `corsMiddleware+Routes.Use` | 默认 `open`：原样回显 Origin + `OPTIONS 204` 全开放 |
| `c7c2c45` | `web/src/services/connection.ts`、`storage.ts`、`net/api.ts`、`net/authGate.ts`、`net/bootstrap.ts` | 去 token、`api.ts` 无鉴权 `fetchJSON`、`bootstrap` 直通 `ready` |
| 2026-10-08 | 删 `web/src/services/e2ee.ts`、`server/internal/e2ee/` | e2ee 那半边改由 **G-H** 整体删除承担；本组只剩「不鉴权 + CORS 开放」这条**策略** |

- 保留判据：**「API 层不做鉴权」是用户明确决策**（多账户 = 多配置档，不是安全边界，AGENTS.md 事实 12）。
  上游把 token 校验或 CORS 白名单合回来 → 所有前端请求 401 / 被浏览器挡下，
  症状是「页面能开、列表全空」。
- 针对性测试：`server/internal/api/http_open_api_test.go` —— 无凭证 `GET /api/dirs` 必须 200；
  `OPTIONS` 必须 204 且原样回显 Origin。**已做变异验证**：把 `handleDirs` 改成返回 401、
  把 `corsMiddleware` 的 `shouldEmit` 关掉，两条断言分别变红。

> 与 G-J 的 `runtime.ts/nodeRegistry.ts` 同提交但 hunk 互斥：鉴权相关归本组，路由/聚合归 G-J。

### G-J 多节点聚合与路由

| 来源与 hunk | 文件 | 说明 |
|-------------|------|------|
| `c7c2c45` | `web/src/services/nodeRegistry.ts`（PALETTE/CRUD/聚合开关）、`web/src/components/NodeSwitcher.tsx`、`web/src/hooks/useNodeRegistry.ts`、`web/src/services/runtime.ts/base.ts/main.tsx` | 节点注册表引入、`mindfs_nodes` 存储迁移、`runtime/base` 按 `nodeId` 路由、`main` 登录直注册 |
| `2d814b2` | `web/src/services/nodeRegistry.ts:normalizeNodeURL` + `web/src/services/runtime.ts:prefix 归一` | 裸 origin 节点在 `/mindfs` 页面自动补前缀 |
| `7a12971` | `web/src/services/rootNode.ts`、`session.ts:connect(ws,nodeId)/buildWSUrl`、`git.ts:fetchGit* nodeId`、`agents.ts/candidates.ts/codexRateLimits.ts/download.ts/file.ts/scheduledTasks.ts/tasks.ts/upload.ts`、`web/src/App.tsx:mapManagedRootsToEntries/_nodeId/_nodeColor + getNodeIdForRoot` | `appURL(...,nodeId)` 与 `rootNode` 映射打通跨节点会话/代理/文件/Git |
| `3d3417a` | `web/src/services/scope.ts:scopeKey/treeKey`、`web/src/App.tsx/FileTree.tsx/SessionList.tsx/session.ts` 的 `scopeKey` 全链路 | 按 `nodeId:projectId` 隔离（与 G-S 的折叠 hunk 分离，此处仅键与路由） |
| 2026-10-15 | `web/src/app/useAppSessionCore.ts` 复用 `appSession.ts:applyPendingToMultiProjectGroups` | **可见症状**：同一 root ID 位于多个节点时，实际在 PC 回复的会话列表蓝灯可能熄灭、切节点跳动或显示在本机副本。**为什么必须保留**：pending 轮询按实际请求节点写 `scopeSessionKey(nodeId, rootId, sessionKey)`；投影必须按 group 自身 `_nodeId` 读同一键，不能从 root ID 重新解析节点。 |

**针对性测试**：`web/tests/cross-node-replying-state.test.mjs` — 同 root/session 在 home 与 pc 仅一侧 pending 时，只允许该节点分组点亮；同时钉住 core 必须复用 node-scoped shared projection。

### G-K 部署前缀与代理基础设施

| 来源 | 文件 | 说明 |
|------|------|------|
| `ecb2ad8` | `internal/deploy/prefix.go`、`server/internal/api/prefix.go`、`server/internal/nodes/store.go`、`server/internal/api/http_nodes.go`、`web/src/services/prefix.ts/nodeBase.ts` | 前缀规范化单一事实源（后/前各一）、`nodes/store` 聚合、`http_nodes` 反代、`nodeBase.normalize` |
| `306f588` | `Makefile`、`cli/cmd/mindfs.go`、`scripts/build-all.sh`、`server/internal/api/appcontext.go/http.go/ws.go`、`server/internal/relay/service.go` | 构建与 CLI 前缀收敛、`ws` 透传、`relay` 前缀 |
| `e92fecf` | `web/index.html`、`web/vite.config.ts:base/scope`、`web/src/services/base.ts/runtime.ts` | Vite `base=/mindfs/`、HTML 基路径、前后端基路径收敛、`web/tests/deploy-prefix/node-url-normalize` |
| 2026-10-09 | `internal/deploy/prefix.go:RelayAssetsAlias`(删)、`server/internal/api/prefix.go`(别名分支删)、`web/src/services/net/prefix.ts:RELAY_ASSETS_PREFIX`(删)、`web/src/main.tsx`、`web/vite.config.ts:RELAY_ALIAS` + SW `/n/<token>/` 归一化(删) | 本组原先还带一条 **relay 专用**的旁支：为了让 relay 改写过的 `/<前缀>-assets/` bundle 仍能取到，前后端各留了一个别名。产生者已随 G-H 删除，这条旁支于同日整体砍掉（详见 G-H 的 2026-10-09（二）行）—— 本组现在只剩「部署前缀本身」这一件事，`web/tests/deploy-prefix.test.mjs` 的断言也翻成了「别名必须不存在」 |

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
| 建会话即写 agent 绑定（`EnsureAgentBinding`） | `server/internal/session/manager.go:Create` / `manager_agent_bindings.go:EnsureAgentBinding/ensureAgentBindingUnsafe` + `server/internal/api/usecase/session.go:SendMessage` | **可见症状**：状态圆圈右下角的 agent 徽标（会话列表、看板任务卡片）在首轮跑完前只显示占位「AI」。原因：列表行是 meta-only 的（G-E），agent 只能由 `InferAgentFromSession` 的「`AgentCtxSeq` 兜底」推出，而它靠 `session_agent_bindings` 回填 —— 绑定原先**只在回合结束**时写。**为什么必须保留的这一条**：与 G-Q 的空 id 守卫是同一块改动面 —— 占位行 `agent_session_id=''`，靠 `manager_aliases.go:upsertExternalSessionNameUnsafe`（空 id 直接 return）和 `lookupSessionAliasForAgentUnsafe`（空 id → not-found）挡住，否则两个都没跑过的同 agent 会话会按空 id 串成同一个别名。合上游时两者要么一起留、要么一起弃。占位行的 `agent_ctx_seq` 必须是 0：非 0 会被 `prependSwitchHint` 当成「已同步到此行」而吞掉 agent 切换提示 |
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

| `web/tests/fork-session-name.test.mjs` | 从拼接源码里抠出 `forkSessionDisplayName` 丢进 vm **执行**，断言「自定义名 > 原父会话#seq > 父会话名#seq > Fork#seq」四级回退。拆分把它搬到 `components/session/sessionListShared.tsx` 并加 `export` 后，测试改为剥掉 `export ` 前缀再 transpile（断言语义不变） |

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
| `web/tests/e2e-stream-ownership.test.mjs` + `tests/e2e/harness.mjs:readBlocks` | **隔离实例 E2E**（`MINDFS_E2E=1` 才跑）：WS 建免费 command 会话 → 浏览器同抽屉再发一条 → 同一抽屉增长 → 刷新稳定 → 落盘块流含两轮内容。落盘断言走 `blocks`（方案 D1 后 `exchanges[].content` 不再写正文，走它会得「内容丢失」假阳性） |


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
- **空工作区的写操作必须无副作用（2026-10-09）**：可见症状是**加项目报「这个账户在本机没有数据」，
  但项目却出现在列表里**（点进去什么都没有）。根因：`fs/registry.go` 的 `Upsert/Remove/UpdateDisplayName`
  都是「先改内存、再 `saveLocked()`」，落盘失败不回滚 ⇒ 失败的写入留在列表里成为幽灵条目。
  只读空工作区（`path==""`）是这条路径的高频场景：陌生 `user=`（如浏览器里过期的账户 id）
  拿到只读空工作区，加项目必然 400，而每次 400 都在内存里留下一笔。`Rename` 一直有回滚，
  现统一走 `snapshotLocked()`。必须保留的理由：这是「本机没有这个账户」这一语义**唯一**可信的地方 ——
  错误提示说没数据、列表却显示有，用户无法判断哪个是真的。
  针对性测试：`server/internal/fs/registry_rollback_test.go`（三个方法各一条：落盘失败后
  upsert 不留幽灵条目 / remove 不误删 / display_name 回滚）。

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

- 来源：AGENTS.md 事实 13 + §0-B 的架构级取舍。**这是最容易被上游合并冲掉的一组。**
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

- 来源：`458495b`（WS 事件处理器整块搬出）、`e7a31d2`（流写缓存抽出）等一连串 refactor、
  `b0ea337`（领域派生、会话时间线、会话标识 helper 继续拆分）。
- 边界：`App.tsx` 原为 ~15k 行巨文件，拆成 `web/src/app/`（19 模块）+ `web/src/renderer/`。
  §0-B-H02/H13 的两大段（上游 module-level helper 与巨型 effect）取 local。
- 本轮继续拆分但不改公开 facade：`useAppDomainLayer.ts` 的派生/恢复阶段抽为
  `useAppDomainDerived.ts`；SessionViewer 的时间线渲染抽为 `SessionViewerTimeline.tsx`；
  `session.ts` 保留 `SessionService` 与所有导出，只把无状态的 client/request id 生成抽到
  `sessionIdentifiers.ts`。Hook 总序列仍为 258，相关 source-map 已登记。

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
- 边界：共享范围表见 AGENTS.md 事实 12 —— 偏好/节点表/WebPush 订阅/提示词/看板模板**共享**；
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
- 边界：`ship` 一条命令完成门禁→提交推送→本机编译安装→推送产物到 WSL 与 fn→两个 worker 重启→三端对账。
  `73db73e` 修的是「不再整目录删除 web 哈希资源」。合上游后 `Makefile` 的本地 target 要保住。
  **WSL 与 fn 是纯 worker，只收二进制与两个 json，不收 `web/dist`**：worker 按设计不服务
  静态资源（`GET /` 403），前端只装本机；曾推的那份是留给「角色翻成 control」的保险，已不成立。
- 可见症状（没有它会怎样）：
  - **改了根级文档跑完 ship 却没被提交**：`git add -A --` 的路径是**白名单**，
    `docs/` 只够得到 `docs/` 目录、够不到仓库根。改了 `AGENTS.md` 再 `deploy-all.sh -m "..."`，
    它会被静默留在工作区，版本号因此挂上 `-dirty`（`git describe --dirty`，2026-10-07 实测）。
    白名单里的 `|| git add -A` 兜底**实际永不触发**：`git add -A -- <存在的路径>` 恒成功。
  - **部署脚本按上游布局整目录删 web 哈希资源**：会让部署前就打开的页面懒加载 404。
- 为什么必须保留：白名单是**故意**的（防 `.mindfs/` 运行期数据被卷进提交），
  所以修法是「把该收的根级文档逐个点名」，不是换成 `git add -A`。
  根级文档中 `AGENTS.md` / `README.md` / `README.zh.md` / `release-notes.md` 必须在内；
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
    字节数用**原始文本长度**记账（`Content-Length` 拿不到：服务端一次性
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

- 来源：`web/src/components/{TaskCardRows,TaskDetailPanel}.tsx`、`web/src/components/task/taskIcons.tsx`、
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
  `web/src/components/{ActionBar,FileTree,SessionList,SessionViewer,GitDiffViewer,DefaultListView,FileViewer}.tsx`、
  `web/src/components/action/actionTypes.ts`、`web/src/App.tsx`、`web/tests/task-board-view.test.mjs`。
- 边界：移动端呼出左右侧栏的两个切换按钮，从 ActionBar（footer）搬进**中间主栏**各面板
  共用的 `ViewHeader` 外壳的两侧，合为一组 —— 按钮位置、ViewHeader 的渲染分支、ActionBar
  的 hideComposer 分支共享同一条「按钮在哪」的判断，合上游时要么全留要么全弃。
  侧栏自身（FileTree / SessionList）传 `showToggles={false}`，不显示切换按钮。
- 可见症状（没有它会怎样）：
  - **移动端进看板/工作台后开不出侧栏**：按钮原本只在 ActionBar 的 hideComposer 分支里，
    上游若把该分支改成 `return null`（或删掉移动按钮），这两个界面就再也呼不出侧栏。
  - **打开具体文件或插件预览后按钮消失**：这些文件态分支各自手写 header，绕过
    `MobileSidebarToggleContext`，移动端无法从文件内容页切换左右侧栏。
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
    DefaultListView / FileViewer）默认 true；侧栏（FileTree / SessionList）传 false。
    App 内直接渲染的插件文件预览也必须使用 `ViewHeader`，不能另写一套裸 header。
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
    6 个视图都使用并闭合 `<ViewHeader>`、插件文件预览也使用共享 header、按钮 22×22、侧栏 `showToggles={false}`、
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

- 来源：`server/internal/session/{types.go,manager.go,manager_session_list.go}`、`web/src/services/{session.ts,pins.ts}`、
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
  3. **窗口轻压缩**（`types.go:CompactExchangeAuxLight` + `manager_aux_reads.go:loadExchangeAuxWindow`）：
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

### G-AY pending 纯派生：done 丢失时停止符号 /「正在思考」卡住（2026-10-07）

> **2026-10-09 补完（最后一处漏网的投影）**：本次纯派生漏了 `useSessionStream` 的本地
> `isStreaming`（`web/src/hooks/useSessionStream.ts`）。它只由 WS 事件清
> （`session.done`→`onDone`、`message_done`、`error`），**没有轮询对账** —— 于是出现
> 「列表蓝灯与输入框停止键已被 5s 轮询救回，唯独流里『正在生成』永久不灭」的半死不活形态
> （连带最后一条隐藏时间戳、A/B 轮措辞错）。治法不新设状态，只收闸门：导出的
> `isStreaming` 一律是 `isStreaming && !!sessionPending`，显示与否只由唯一真相
> `pending` 决定，本地标记退化为「措辞 / 盒子」子状态；`SessionViewer` 的显示条件
> 相应去掉 `|| isStreaming`。`pending-single-source.test.mjs` 加第 7 节钉住这两条
> （把闸门改回裸导出，测试即红）。

> **2026-10-15 补完生命周期边界**：`message_done` 只是 agent 消息边界，不能早于带 `MindFSID` 校验的 `session.done` 结束 viewer 的本地 streaming；否则后续辅助事件会造成状态灯 false→true 闪烁。`useSessionStream` 只在 `message_done` 清状态文字，仍由 `session.done` / error 终止；`use-session-stream.test.mjs` 覆盖该顺序与 pending 总闸门。

- 来源：`web/src/App.tsx`（`multiProjectPendingByKey` 成为唯一真相；`setMultiProjectSessionPending`
  同步更新 ref 再 setState；三个读点 `getSessionSnapshot` / `resolvePendingForSession` /
  `rootSessionIndicators` 改为派生）、`web/src/app/useRealtimeEvents.ts`（`handleSessionStreamDone`
  不再往会话对象写 pending）、`web/src/app/appSession.ts`（删掉补丁 `clearStalePending`；
  `mergeReplyingStateByNode` 仍是服务端真值的增量合并点）、
  `web/tests/pending-single-source.test.mjs`（新增）。
- 边界：**「这个会话在不在回复」这一份状态的存放方式**。合上游时若退回「会话对象上存 `pending`」，
  就又会裂成多份、又会在丢 `session.done` 时永久卡住 —— 正是本 bug 的可见症状。
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


### G-AY 抽屉 pending 对账：done 丢失时停止符号 /「正在思考」卡住（2026-10-07）

- 来源：`web/src/app/appSession.ts`（新增 `clearStalePending`）、`web/src/app/useReplyingSessionPoll.ts`
  （新增依赖 `multiProjectPendingByKey` 的 effect；2026-10 拆分前在 `web/src/App.tsx`）、
  `web/tests/drawer-pending-reconcile.test.mjs`（新增）。
- 边界：**抽屉 / 选中 / 缓存三处的 pending 对账**。这三处的 pending 只由
  WS `session.done`（`handleSessionStreamDone`）清除；合上游时这块 effect 被冲掉，
  done 一丢失就永久卡住，而列表蓝灯因为另有对账不受影响 —— 正是本 bug 的可见症状。
- 可见症状（没有它会怎样）：
  1. 任务已结束，输入框仍显示停止符号（ActionBar `showCancel = !!currentSession?.pending`）。
  2. 查看器仍显示「正在思考」（`isAwaiting = !!session.pending`、`isStreaming` 的初值由
     `sessionPending` 决定，而它只在 `[sessionKey, sessionPending]` 变化时重算）。
  3. 用量 / 上下文窗口面板不出现（画面停在流式态）。
  4. **会话栏是正常的** —— 列表蓝灯每 5s 从 `/api/replying-sessions` 对账一次，
     所以列表收敛、抽屉不收敛，两边看起来「不一样」。
- 根因：
  - `pending` **不落盘**（`sessions` 表无此列，`sessionListResponse` 也不发），纯前端 optimistic。
  - 抽屉（`drawerSessionByRootRef` → `currentSession`）的 pending 只有 `handleSessionStreamDone`
    一个出口。`BroadcastSessionDone` 只发给 `session.ready` 绑定的客户端
    （`GetSessionClientIDs`），而 WS 连接在本机反复 `1006 unexpected EOF` 断连 ——
    重连后若没重新绑定（或绑定竞态），`session.done` 就打不到这个客户端。
  - 实测（journalctl）：`1791307447-6f646793904d` 在 11:57:12 广播了 `session.done`，
    但**其后没有 `GET /api/sessions?root=…` 列表重拉** —— 而 `session.done` 处理器里
    `handleSessionStreamDone` 之后**无条件**调 `scheduleSessionListReload`。没有重拉
    就证明该处理器没跑，即这条 WS 事件没被前端处理。会话列表靠 11:59:21 重连 +
    5s 轮询才收敛，所以「会话栏正常、抽屉卡住」。
- 改了什么：
  - `clearStalePending(session, isServerPending)`：服务端说「不在跑」就清 `pending`，
    无变化时返回**同引用**（调用方靠 `!==` 判断是否需要 setState）。
  - `web/src/app/useReplyingSessionPoll.ts` 新增 effect（依赖 `multiProjectPendingByKey`）：对抽屉 / 选中 / 缓存
    三处调 `clearStalePending`，真值取自 `multiProjectPendingByKey` —— 也就是
    `/api/replying-sessions` 的落点，**与列表蓝灯同一个源**。这样 done 丢了也能在
    下一个 5s 轮询周期收敛，且不会把「节点没拉到」误当成「没在跑」。
- 针对性测试：
  - `web/tests/drawer-pending-reconcile.test.mjs` — 钉住「服务端说不在跑就清、在跑就留、
    无变化返回同引用、按 key 判定」，并源码守卫「逻辑模块 `src/App.tsx`（source-map 拼接，
    含 `useReplyingSessionPoll.ts`）必须有依赖 `multiProjectPendingByKey` 的 effect 且对三处调
    `clearStalePending`、真值必须读 `multiProjectPendingByKey`」。**已验证：把 effect 删掉后该测试立刻变红。**
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
  `7b6d969`（services/{task,git,file} 拆成目录模块）、`def8b7e`（FileTree 图标子组件抽出）、
  `5524e8d` / `811e0fa`（App.tsx 区域渲染与 effect 拆分）、
  `b548f93` / `c4bb177` / `82d690e` / `d19ba81`（App.tsx 分层聚合 4 层）、
  `e043a5e`（抽离 `AppRootView`，App.tsx 1267 → 16 行）、
  e2e 冒烟夹具（`e2e/conftest.py` + `e2e/test_app_smoke.py`，运行时安全网）。
- 边界：**前端目录结构与文件拆分**。`components/` 下 40+ 个 `.tsx` 平铺 →
  `components/{session,task,file,git,root,agent,account,shell,common}`；
  `services/` 下 20+ 个 `.ts` 平铺 → `services/{net,platform,prefs,task,git,file,session}`；
  会话域实现归入 `services/session/`，根路径 `services/session.ts` 只重导出公开面；
  纯键工具 → `shared/`。`services/{task,git,file}.ts` 各拆成目录模块，
  原路径保留 barrel shim（`export * from "./x/index"`）。
  **拆分策略是「机械搬移 + 旧路径 barrel 重导出，零行为变化」**：对外导出面不变，
  只搬文件。43 条源码正则断言（读旧逻辑路径）由 `web/tests/source-map.mjs`
  统一读层兜住（逻辑路径 → 物理文件列表，`readSource` 拼接）。
- 可见症状（没有它会怎样）：上游把文件放回扁平结构（`components/*.tsx`、`services/*.ts`
  平铺）时，本地拆出的分域目录与 barrel shim 被整份覆盖 —— 43 条源码正则断言与
  barrel 重导出同时失效，会话/任务/文件/git 四域的模块边界消失，导入环与
  「同一判定两处实现」这类重复悄悄回来（文件级 diff 看不出来）。会话模块若重新散落在
  `services/` 根目录，目录职责会退回扁平状态，facade 与实现边界也无法一眼辨认。
- 理由：前端原为扁平巨文件结构，单文件最大 10472 行（`App.tsx`）。按域重组后
  模块边界 = 职责边界，是后续 App.tsx 拆分的地基。
- 验证：`web/tests/frontend-split.test.mjs`（barrel 完整重导出 + session 域只留根 facade + 无导入环 +
  source-map 映射）、`web/tests/source-map.test.mjs`（读层完整性）、
  `web/tests/agent-lifecycle-restart.test.mjs`（分层后 `restartAgent` 的 import 落点）、
  `e2e/test_app_smoke.py`（Playwright 运行时冒烟，10 用例，**刻意不进常规门禁**）。
- 分层聚合（2026-10-08）登记说明：`App.tsx` 拆成四层聚合 hook + `AppRootView`。
  `web/src/app/` 整目录前缀已在本组 `files` 里，所以 5 个新模块自动被覆盖；
  四个层模块与 `AppRootView` 也都进了 `web/tests/source-map.mjs` 的
  `"src/App.tsx"` 数组（逻辑路径 → 物理文件列表），43 条源码正则断言因此继续有效。
  **唯一需要手工补的是 `web/tests/agent-lifecycle-restart.test.mjs`**：它原先与上游
  同版（不是 delta），分层把 `restartAgent` 的调用点搬进 `useAppSessionLayer` 后它才变成
  delta，于是断言改指那个模块，并登记进本组 `tests`。
- e2e 安全网：`e2e/` 用隔离配置目录（`XDG_CONFIG_HOME` 指向临时目录）+ test 专属临时账号
  （`e2e_test`）+ 随机端口，不碰真实机器状态；给 App.tsx 拆分提供「跑起来没坏」的验证。
  已知上游问题：视图交互 + 页面重载累积 8~9 次后后端整体失响应（task-35 未改后端一行），
  故用例间改用应用内导航复位。详见 `e2e/README.md`。
- 已知边界：`session.ts` 曾于 2026-10-07 暂不拆分（VM 执行类测试依赖单文件自包含），
  2026-10-10 完成拆分：WS 连接生命周期 + 消息收发搬进 `sessionConnectionManager.ts`
  （633 行），`SessionService` 只保留高层编排（479 行）。`source-map.mjs` 已登记新文件，
  `session-core-unit.test.mjs` 等源码正则断言零改动继续生效。
- WS 事件处理器拆分（2026-10-10）：`realtimeWsHandlers.ts` 的 `session.user_message`
  handler（195 行）搬进 `realtimeWsUserMessage.ts`（239 行）；`session.accepted`
  handler（104 行）搬进 `realtimeWsSessionAccepted.ts`（146 行）；`root.changed`
  handler（56 行）搬进 `realtimeWsRootChanged.ts`（93 行）。
  `realtimeWsHandlers.ts` 从 883 行降到 533 行。`source-map.mjs` 已登记新文件。
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

### G-AX pending 信号兜底（ClearSessionPending 2s 超时 + 广播先于清；2026-10-08 建，同日修订：删掉第②③层）

> 2026-10-11 由 `G-AY` 改名而来：原先与「pending 纯派生」共用 `G-AY` 一个 id，
> 而门禁不校验 id 唯一性，`git log --grep 'Scope: G-AY'` 会同时命中两组、无从区分。
> 取空闲的 `G-AX`（沿用至今未占用的号），md/yaml 与正文引用一并改。

- 来源：`server/internal/api/stream_hub.go`、`server/internal/api/appcontext.go`、
  `web/src/App.tsx`、`web/tests/e2e-pending-signal.test.mjs`。
- 边界：**「会话完成后 pending 信号必须消失」这一行为的实现方式**。
- 可见症状（没有它会怎样）：
  对话完成后仍显示「正在生成」+ 停止键，用户以为还在跑，实际早已完成。
- **根因（2026-10-09 修订）**：本条原先记的「`ClearSessionPending` 自旋无超时」**是误判** ——
  2 秒 replay-drain 超时早就存在，广播次序也早已在清之前。两个相反症状（灯不灭 / 灯不亮）
  的真正结构性成因是 **pending 条目没有回合身份**，已另立 **G-BE** 承接。
  本条保留的是「必须存在超时、不得退回无超时自旋」这一**行为约束**（局部防护），
  它不再是任一处症状的根因说明。
- 为什么必须保留：
  只剩第①层（必修）：服务端 `ClearSessionPending` 的 replay-drain **必须有超时** + 广播先于清。
  合上游时若退回「`ClearSessionPending` 无超时自旋」，replay 客户端排不空时仍会卡住。
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

### G-BE pending 的回合身份与单一终结器（2026-10-09）

- 来源：`server/internal/api/stream_hub.go`、`server/internal/api/appcontext.go`、
  `server/internal/api/ws.go`、`server/internal/scheduled/tasks.go`、
  `server/internal/agent/codex/session.go`（2026-10-09）。
- 边界：**服务端 pending 条目的身份与生命周期** —— 谁有权清理、迟到/重复 done 的处置、
  子会话 pending 的终态收边。不做静默判死 / 超时（超时路线已于 G-AX 证伪）。
- 可见症状（没有它会怎样）：两个**方向相反**的症状并存 ——
  ①「完成回答但还显示正在思考」（灯不灭）；②「正在运行但会话列表灯不亮」（灯不亮）。
  **同一个缺失的两面**：`pendingSessions[key]` 没有回合身份、清理是不问归属的无条件删。
  - 灯不亮：第 A 轮迟到的 `session.done` 落地时，`ClearSessionPending(key)` 无条件删条目，
    把第 B 轮刚开始的灯抹掉（该 done 帧本身也会让前端按 key 清灯）。
  - 灯不灭：子会话只有 `MessageDone` 一条清理边，error / 取消 / 父轮先结束都让 pending 永久残留；
    `runSessionMessage` 无 `defer`，`SendMessage` panic/挂死时终结器走不到。
- 为什么必须保留：
  1. `SessionPendingState` 带回合身份（G-BH/S2e 起是 `TurnID string`）；`SetPendingUserAt`/`SetPendingReply`
     每次刷新，并把该身份返回给调用方（`BroadcastSessionUserMessageAt` 同样返回）。
  2. `ClearSessionPending(key, turnID)` 仅在「两侧都有身份且一致」时清；`PendingTurnIDMatches`
     让终结器对「别的回合的 done」整条丢弃（连帧都不广播），迟到 done 彻底无害化。
     `turnID==""` 视为无主，保守清 —— 绝不因缺身份而漏清（漏清 = 灯不灭）。
  3. 抽出唯一终结器 `AppContext.EndSessionTurn(rootID, key, requestID, turnID)`，
     所有清理点都改调它；`runSessionMessage` / `RunAgentStage` / 定时任务 用 `defer`
     覆盖成功/出错/取消/panic。**无身份的入口现在有两层护栏**：① `AppContext` 那层
     兼容壳 `BroadcastSessionDone`（转调 `EndSessionTurn(..., "")`）**已删除**，定时任务曾是
     它最后一个调用点；② `StreamHub` 那个真正广播的方法**刻意降为未导出**
     `broadcastSessionDone` —— 它只有 `EndSessionTurn` 一个调用点（身份门之后才调），
     不导出就没有任何路径能绕过身份门广播 done。用编译器守，比只扫某个调用点硬。
  4. 子会话在父轮收尾时一并终结（本轮创建、没等到 `MessageDone` 的那些）。定时任务路径
     （`scheduled/tasks.go`）从 `OnStart`/`OnSubSessionCreated` 捕获每会话的 `turnID`，
     收尾（含 `defer` 兜的子会话）原样交给 `EndSessionTurn`，不再用无身份的广播。
     接口上的 `BroadcastSessionUserMessage`（不带 `At`、不返回身份的那个）**已摘除** ——
     它置 pending 时按 `turnID==""` 走，是同一类「拿不到身份」的入口，留着等于给下一个
     调用点挖坑；`StreamHub`/`AppContext` 两侧的无用实现一并删除（全仓库无调用者）。
  5. `codex.handleStreamedEvents` 由「通道关闭即成功」改为返回是否见到 `TurnCompletedEvent`，
     通道中途关闭按错误处理 —— 否则会**提前/错误**触发 done（本根的相邻缺口，同批修）。
  - 这套「身份比对」机制项目里本就有（`ActiveTurnID` 用于取消定位），
    pending 缺的不是新机制，是把同一份纪律用上去。前端早已单一源（`multiProjectPendingByKey`），
    故本组不改前端。
- 针对性测试：
  - `server/internal/api/stream_hub_turn_identity_test.go` → 迟到 done 不清新轮（`TurnID` 比对）、
    重复 done 幂等、`turnID==""` 仍保守清、子会话由父轮终结、`PendingTurnIDMatches` 缺失时放行。
  - `server/internal/scheduled/pending_identity_test.go` → 定时任务路径的源码守卫（四条）：接口里不得再有
    无身份的 `BroadcastSessionDone`、置 pending 的两个调用点必须接收 `turnID`、
    `EndSessionTurn` 不得收到哨兵身份（新旧哨兵 `""`/`0` 都拦）、不得再出现不带 `At` 的
    `BroadcastSessionUserMessage(`。钉住「定时任务收尾也带回合身份」这条补齐（合上游时若退回
    无身份广播，此测试必红）。
  - `server/internal/agent/codex/session_test.go` → 通道关闭无 `TurnCompleted` 报错、见到 `TurnCompleted` 返回成功、
    `ThreadError` 返回错误。
  - `web/tests/e2e-pending-indicators.test.mjs`（`MINDFS_E2E=1`，隔离实例）→ **浏览器 + 真后端**层：
    ① done 后等待指示器消失 + 列表灯灭、运行中灯亮过；② **队列接力**（A 在跑时发 B 排队）
    期间「服务端在跑但 UI 灯灭」连续不得超过 1 次采样 —— 这是「迟到 done 不误抹新轮」
    唯一的过程级证据（单测与源码契约层都够不到真实时序）。

### G-BF Full 同步丢最新几轮：ctx_seq 切片 + 时间地板失效（2026-10-09）

- 来源：`server/internal/agent/claude/importer.go:readClaudeImportedExchangeLocators`、
  `server/internal/api/usecase/external_sessions.go:SyncExternalSessionDelta`（2026-10-09）。
- 边界：**用户手点「同步」（Full）时，live-owned 会话「比库里最新一条更新」的尾段
  导不进来**。只影响 Full 同步的增量圈定；非 Full 的常规增量同步、import-owned 会话、
  子代理同步、repoint 游标推进都不动。
- 可见症状（没有它会怎样）：手点「同步」后，会话**最新几轮回复永远补不回来** —— 不是
  「导重复」，是「一条都导不到」。实测某会话库 119 行、导入器全量只有 83 条（实时路径
  会写导入器不产出的 tool-only 空占位行），`agent_ctx_seq` 被刷成库长度 115，
  `imported[115:]` 恒为空 → `ImportedCount` 恒为 0，用户反复点「同步」也白点。
- 为什么必须保留：
  1. `readClaudeImportedExchangeLocators` 只在 `committedOffset > 0` 或 `bootstrapAfter`
     非零时应用时间地板；而 Full 同步恰恰是**两者都不传**的组合（`importInput.Cursor`
     只在 `!in.Full` 里设、`AfterTimestamp` 同理），于是 `TimestampFloor` 被静默跳过。
     这与 `types.go` 里 `TimestampFloor` 的契约（「非零时无论有没有字节游标都只接受
     比它晚的条目」）直接矛盾。修复：只要地板非零就过滤。
  2. `SyncExternalSessionDelta` 的 Full 分支原先**无条件**按 `binding.AgentCtxSeq` 切片。
     `agent_ctx_seq` 的语义是「导入器条数游标」，但代码把它刷成了 `len(latest.Exchanges)`
     （**库行数**）—— 对 live-owned 会话这两个数系统性不相等（库行数 > 导入器条数），
     切片 `imported[ctx_seq:]` 于是恒空，把第 1 条的地板修复也一起废掉。修复：
     live-owned 的 Full 同步改由地板圈定增量、**不再叠序号切片**；import-owned 的库与转录
     按序号对齐，`ctx_seq` 切片仍是正确边界，保持不变。
  3. 两个写入者（实时路径 / 转录导入）的幂等护栏 `exchangeAlreadyRecorded` 仍在：
     地板只放行「比库里最新一条更新」的条目，这些条目必然不在库里，护栏不会误伤；
     真出现内容相同但时间戳差 >5s 的重尾，没有投影（G-AB 已退役）可折叠，由 native id 身份判重拦住。
- 针对性测试：
  - `server/internal/agent/claude/importer_test.go` → `TestReadClaudeImportedExchangesAppliesFloorWithoutCursorOrBootstrap`：
    `committedOffset==0` 且 `bootstrapAfter` 为零、只有地板非零时，地板必须生效
    （把 15:00 的两条滤掉、只留 15:31/15:32 的两条）。旧代码在这里直接 `return items`，
    此测试必红。
  - `server/internal/api/usecase/external_sessions_test.go` → `TestSyncExternalSessionDeltaFullImportsTailWhenCtxSeqOvershoots`：
    live-owned 会话库 4 行、`ctx_seq` 被刷成 10（超过导入器 6 条），Full 同步仍要导入
    尾段 2 条（`ImportedCount==2`、库 6 行）。旧代码 `imported[10:]` 为空，此测试必红。
  - 既有 `TestSyncExternalSessionDeltaFullDoesNotReplayHistoryInSession` /
    `TestSyncExternalSessionDeltaSetsFloorForLiveOwnedOnly` /
    `TestSyncExternalSessionDeltaFullImportsSubagentTurns` 保持绿 —— 钉住「历史尾段不重放、
    live-owned 必带地板、子代理多回合仍导」这三条不被本次修复打破。

### G-BC 方案 B：worker 没有账户表，账户 id 从用户名派生

- 来源：`server/internal/auth/auth.go`、`server/app/workspace.go`（2026-10-08）。
- 边界：**worker 不加载账户表也能做数据分区** —— 账户 id 从用户名派生
  （`auth.DeriveAccountID`），跨机器稳定；worker 上 `Resolve` 返回空时派生 id、
  按需建目录，不回空工作区。
- 可见症状（没有它会怎样）：
  在 fn（worker）上加/删项目时报 `registry path required`（或 `这个账户在本机没有数据`），
  因为 worker 没有账户表，`user=xiaokubao` 解析不出目录名，落到 `emptyWorkspace`
  的空注册表上，写操作撞上 `path == ""` 的内部错误。
- 为什么必须保留：
  worker 有账户表 = 控制面漂移（每台机器一份 users.json），正是多节点改造要消灭的东西。
  派生 id 让 worker 无表也能分区，且跨机器稳定（同一用户名永远算出同一目录名）。
  启动时 `migrateIDsToDerived` 把存量随机 id 迁移成派生 id（重命名目录 + 更新 users.json）。
- 针对性测试：
  - `server/internal/auth/auth_test.go` — `TestDeriveAccountIDStable`（同用户名跨调用结果一致）、
    `TestDeriveAccountIDDistinct`（不同用户名 id 不同）、`TestMigrateIDsToDerived`（旧 id 迁移 + 目录重命名）。
  - `server/app/workspace_test.go` — `TestWorkspaceWorkerDerivesAccountID`（worker 角色下
    `Resolve` 返回空时派生 id、走 build 不回空工作区）。

---

### G-BG 会话窗口读不整表解析 + tail 增量三处缺陷（2026-10-10）

- 来源：`server/internal/session/manager.go`、`server/internal/api/http.go`、
  `server/internal/api/usecase/session.go`（2026-10-10）。对应主方案 S1（`docs/session-blocks-plan.md` §12）。
- 边界：**打开/切换会话时的窗口读（`/api/sessions/{key}?latest=` / `?before_seq=`）与列表页
  的 shell 标签推断**，必须 O(窗口) 或 O(尾部命中)，不得整表解析、不得全量读 aux。
  实时写路径、Full 同步、import、repoint、子代理同步都不动。
- 可见症状（没有它会怎样）：
  1. **打开会话/切换会话时整个 root 停顿**：冷缓存开窗走 `readExchangesFull`，解析整份主日志
     且持 `m.mu`。实测 1.19MB/206 行 15.3ms；会话越大越久。前端每 ~1.7s 轮询一次窗口端点，
     等于每隔一秒多就有人把 root 锁住十几毫秒 —— 用户体感就是「同步后内容消失/界面卡住」。
  2. **同 seq 的后续版本读不到**（`manager_exchanges.go:readExchangeTail` 曾按 `baseSeq` 跳过旧 seq）：
     tail 追加若含与已缓存 seq 相同的更新（更高 revision / 更完整），增量快路径会直接跳过它，
     页面继续显示旧内容；只有冷启动才碰巧看到对的。
  3. **tail 内同 seq 重复吐**（`manager_exchanges.go:loadExchanges` 曾盲 append）：同一 seq 追加多行时窗口里出现重复条。
  4. **完整性升级被误报成冲突**（`manager_exchanges.go:mergeExchangeRecord`）：同 seq 的空占位被后来的完整回复覆盖时打
     `conflicting duplicate seq` 告警，污染日志 —— 空正文永不覆盖非空正文，只有「已有正文非空且与新正文不同」才算真冲突。
  5. **含超长行的会话整个搜不到**：搜索的 `bufio.Scanner` 上限写死 1MB，而主日志解析上限是
     `maxExchangeLineBytes`（64MB），任一单行 >1MB 都会让 `Search` 返回 `ErrTooLong`。
  6. **列表页对每个命令会话全量读 aux**（`fillCommandShells` → `GetExchangeAux(key, 0)`）：
     实测单会话 aux 最大 19MB / 11.8MB 那次 281ms，且持 `m.mu`。
  7. **非窗口化的 sync 全量读 aux 再过滤**（`handleSessionSync` 无条件 `Seq: 0`）：
     `handleSessionGet` 早就按窗口只取窗口内 seq 的 aux（G-AT），sync 这条漏了同一个优化。
- 为什么必须保留：
  1. `getSessionWindowUnsafe` 冷缓存改走 `readExchangeWindow`：读盘一次 → 字节级行计数
     （`bytes.Count` 思路，不解析 JSON，实测 1.2MB/0.14ms）拿到 `Total` → 只解析尾部
     `need+1` 条（多一条只为判 `hasMore`）。`Total` 在冷缓存下取**非空行数**而非 `len(全部记录)`：
     实测 310 份真实主日志 / 2172 行，**0 空行、0 同 seq 多行**，只有 1 份 maxSeq 与行数差 86。
     退化日志（同 seq 多行）会显示「重复条数」—— 可接受，S2d 之后主日志只剩元数据，此代价消失。
  2. 尾部读**刻意不复用 `readAuxWindowTail` 的逐块反向读**：那个实现会丢掉每个分块的首条
     半行，单行最大 308KB 的主日志会被静默丢记录。改成「只读一个后缀、起点对齐换行」的单次读，
     span 按 4 倍增长。
  3. 新增 `mergeExchangeRecord` / `mergeExchangeRecords`：新 seq 追加（与全量读同一
     「按文件序」假定），同 seq **原位替换**且保留原位置；空内容永不覆盖非空内容；
     仅「两条都非空且内容不同」才打冲突告警。`readExchangesFull` 的逐行合并改为调用它 ——
     全量与增量从此共用一套合并语义。
  4. `readExchangeTail` 去掉 `seq<=baseSeq` 跳过，改为**返回全部解析结果**，去重交给
     `mergeExchangeRecords`。理由：这条路径在最热的读取路径上，同 seq 的更新必须看得见。
  5. 新增 `Manager.GetCommandShell`（现位于 `manager_aux_tail.go`）：aux 尾部有界读（512KB 起步、4 倍增长），命中即停；
     `fillCommandShells` 改用它。判据仍是 `InferCommandShellFromAux`（最后一条
     `source=userShell`、`phase=final` 的 commandExecution）。
  6. `handleSessionSync` 改为与 `handleSessionGet` 同构：有 `windowMeta` 就按窗口内 seq 取
     aux（走 G-AT 的 `CompactExchangeAuxLight`），否则维持 `Seq: afterSeq`。
- 针对性测试：
  - `server/internal/session/manager_window_cold_test.go` → `TestGetWindowColdCacheAvoidsFullParse`
    断言 `Manager.fullExchangeReads == 0`（计时会随机器抖动，计数器不会）；
    `TestGetWindowColdCacheBeforeSeq` / `TestGetWindowColdCacheLatestIgnoresLimit` /
    `TestGetWindowColdCacheCoversWholeFile` / `TestGetWindowColdCacheMissingLog` /
    `TestGetWindowColdCacheHandlesLineLargerThanChunk` → 冷开窗的 seq/hasMore/total/边界。
    计数器是新字段：合上游把它删掉这组测试必红。
  - `server/internal/session/manager_incremental_merge_test.go` →
    `TestLoadExchangesIncrementalAppliesSameSeqUpdate`（同 seq 更新必须可见）、
    `TestLoadExchangesIncrementalDedupsSameSeqWithinTail`（tail 内同 seq 不重复吐）、
    `TestLoadExchangesDoesNotWarnOnCompletenessUpgrade`（完整性升级不打冲突日志，真冲突必打）。
  - `server/internal/session/manager_search_test.go` →
    `TestSearchSessionContentMatchesLineLargerThanOneMegabyte`（单行 2.4MB 仍能搜到）。
  - `server/internal/session/manager_aux_tail_test.go` →
    `TestGetCommandShellReadsAuxTailOnly` / `TestGetExchangeAuxWindowReadsAuxTailOnly`
    断言 `Manager.fullAuxReads` 保持 0。
  - `server/internal/api/http_window_test.go` →
    `TestHandleSessionSyncWindowUsesWindowAux`：窗口 sync 必须走轻压缩（edit content 清空、
    展开时懒加载），非窗口 sync 必须保留 content —— 用 G-AT 的压缩契约当可观察判据。
  - `server/internal/api/usecase/usecase_test.go` →
    `TestListSessionsFillsCommandShellFromAuxTail`：列表页仍能正确推出 shell 标签（接线没断）。
  - 既有 `TestGetWindowColdCache*` 同源用例、`TestHandleSessionSyncWindow`、
    `TestGetExchangeAuxAuxWindow`（G-AT）、`TestLoadExchangesMergesDuplicateSeq` 保持绿。
  - `web/tests/session-window.test.mjs`（后端源码契约）→ 开窗拆成「缓存切片 / 冷缓存尾部读」
    两条后，`hasMore` 的断言从「单处赋值」改成**两条路各自都要算出 hasMore**
    （`return all[start:], total, start > 0` 与 `hasMore := len(records) > need`）——
    语义等价且更强，钉住「两条路的 hasMore 都不能丢」。
- 验收记录（实测，合成最坏输入 1.19MB/206 行、每行 ~5.7KB）：冷开窗 **15.3ms → 1.4ms**
  （读盘 0.9ms + 行计数 0.14ms + 解析 9 条 0.8ms）；真实最重会话 391KB/206 行按比例约 0.6ms。

### G-BH 会话正文块流（块存储层 + 轮次身份 + S2a 双写 + S2b 读改走块流 + C1/C2 + S2d-2 主日志不再存正文 + S2e pending 身份改用 turn_id）（2026-10-10）

- 来源：`server/internal/session/blocks.go`（新增）、`server/internal/session/manager.go`、
  `server/internal/session/types.go`、`server/internal/api/usecase/session.go`、
  `server/internal/api/usecase/session_repoint.go`（2026-10-10）。对应主方案 S2a
  （`docs/session-blocks-plan.md` §3/§4/§10）。
- 边界：**会话正文的存储、身份与读取** —— 块流格式、块写入点、轮次元数据字段、`turn_id` 的
  生成与清理、窗口读走块流、前端按 `type` 渲染块。
  S3 退役 `seq=0` 机制 —— 后续步骤，不在本组。
  本组内部 S2a（双写，读路径不动）、S2b（读路径切块流，`content` 退化为回退）、
  S2d-2（主日志 `content` 恒空，块流是唯一载体）、S2e（pending 灯的身份从单调代次
  换成 `turn_id`）是**四个可独立回滚的提交**：
  S2b 之后前端仍能渲染没有块流的历史会话，S2d-2 之后老会话靠 S2d-1 的回填补齐，
  S2e 只换 pending 身份的表示（`uint64` → `string`），不碰任何落盘语义。
- 可见症状（没有它会怎样）：
  1. **回复结束后助手正文消失**：一轮正文只拼 `text` 块，而 `os` 模型约 15% 的轮次只产出
     reasoning、没有任何 text，于是落库成 `content:""`。流式时 `thought_chunk` 实时推所以
     看得见，收尾落库后正文瞬间消失；点同步也救不回（两个 importer 都不收 thought）。
     实测空正文占比：claude::os 39%、dsh::os 13%（claude::of 5%、dsh::of 2%）。
  2. **一轮正文没有权威身份**：主日志只有 `seq/role/content`，没有 `turn_id`，于是
     「同一个回合有两个写入者」只能靠内容比对 + 时间戳窗口猜（投影 rule A/A2/B）。
  3. **repoint / fork 之后 native id 与新转录错配**（`repoint.go:35` 明载 fork 后 claude
     产出全新 uuid）：本方案开始持久化 native id 之后，旧 native id 会让判重失效、回查错行。
  4. **服务端发起的轮次没有 `turn_id` 可采纳**：客户端根本没有传 `turn_id` 的入口
     （`Stream.Send` 只收 string），而定时任务 / 看板任务阶段 / 子会话 / 命令会话都由服务端发起。
  5. **同步把已存在的轮次又写一遍**（用户报告「同步后丢失的更厉害了」的另一半）：
     ACP 只有 `LoadSession`、没有字节游标，Full 同步必然整段重放；而实时写入的行与
     导入重放出来的行同时躲过两道防线 —— 导入器把相邻同角色条目**合并**，`Content`
     逐字不同；ACP 的 `SessionUpdate` **不带时间戳**，导入行盖的是 `time.Now()`，
     与实时行的真实时间能差几小时，于是「内容 + ±5s」和 `TimestampFloor` 一起失效。
     实测 2026-10-07：live-owned 会话 `1791339665-fb5134041392` Full 同步后多出
     **9 对** live/import 重复行，另有 3 对 import+import（相隔 20s）。
  6. **ACP 导入丢 reasoning**：`importer` 只收 `UserMessageChunk` / `AgentMessageChunk`，
     `AgentThoughtChunk` 整个被丢掉，于是「只思考不出话」的轮次导入后正文为空。
  7. **窗口读把 reasoning 整条丢掉**（`CompactExchangeAux`）：两条路都会扔 ——
     只有 `thought` 没有 `toolcall` 的 aux **整条返回 false**；`toolcall` 与 `thought`
     同时存在时 `aux.Thought = ""`。而流式期 `thought_chunk` 是实时推的，所以用户
     「看着有」，关掉重开就没了 —— 正是①的症状。实测存量 **10826 条 / 2657 万字符 /
     17 个会话**的 reasoning 一直在盘上但读不出来。
  8. **S2a 之后写入的数据从未部署**：本机 320 个会话、2166 行，**0 个块流文件**
     （块流要 `make install` 之后的二进制才写）。所以 S2d 直接删 `Exchange.Content`
     等于把全部历史正文清空。
  9. **S2d-2 之前的双写是两份真相**：块流与主日志 `content` 同时写着同一段正文，
     任何一处只被部分更新就会漂移（revision 版本、导入合并、fork 复制各有各的路径）。
     合并本轮起主日志 `content` 恒空，块流是唯一载体。
  10. **user 行被空内容守卫整行吞掉**：S2d-2 一旦让 user 行不再带正文，
     `addExchangeForAgentAt` 里「空正文 + 非 agent 就跳过」的判据会把**每一行 user**
     丢掉 —— 前端表现为「我说过的话不见了」，且没有任何报错。
  11. **S2e 之前 pending 灯用的是另一套编号**：`SessionPendingState.TurnGen` 是 StreamHub
      内的单调计数器，与主日志行 / 块流的 `turn_id`（UUID）**不是同一个东西的两种表示**。
      于是「本轮是哪个回合」这个问题在两处各有一个答案、各有一套生成规则 ——
      方案 §11 引 `message-mechanisms-plan.md` §12 的警告「两套回合身份真值并存必然打架」。
      换成 `turn_id` 后同一个逻辑 id 只有一种表示，`IsTurnIDShape` 也只有一份判据。
- 为什么必须保留：
  1. 新增 `sessions/<key>.blocks.jsonl`（块流），一行一块：
     `{seq,pos,revision,type,text|toolcall|todo|plan|compact,step,turn_id,native_id,parent_tool_use_id,task_id,subagent_type}`。
     `type ∈ {reasoning,text,tool_call,todo,plan,compact}`。`pos` 取代旧 aux 的 `line`
     （`line` 是「在 content 里的第几行」，content 一删就失去意义）；同 `(seq,pos)` 多版本
     按 `revision` 取最高。
  2. `Manager.AppendBlocks` / `ReadBlocks` / `GetBlocksWindow` / `blocksPath`（`blocks.go`）：
     append-only JSONL，坏行跳过不报错（一行写坏不能让整个会话的正文读不出来）。
  3. `Exchange` 加轮次元数据：`turn_id`（MindFS 权威身份，落盘即定、永不改）、`turn_seq`
     （= 打开该轮的 user 行 seq）、`native_turn` / `native_prompt`（agent 侧回查键，可空、
     可后补、fork 后可变更）、`partial`（本轮未收尾）。用 `session.WithExchangeIdentity`
     这个 context 值传递而不是给 `addExchangeForAgentAt` 加参数：identity 是轮次级
     （同一轮的用户行与助手行必须带**同一个** `turn_id`），而调用点已有 8 处。
  4. `BuildTurnBlocks` 把「一轮的正文 + aux」编译成块序列，顺序与前端
     `buildAssistantTimeline` 的既有渲染顺序**逐条一致**（aux 按 `(line,到达序)` 稳定排序后
     插进正文段之间、空正文段跳过）—— S2b 起块序就是渲染序，不一致就等于切换读路径时
     正文顺序会变（用户可见的回归）。放在 usecase 而不是 `Manager.appendExchange` 里做：
     `appendExchange` 只看到一段字符串、`appendExchangeAux` 只看到一条 aux，
     两者都拼不出交错顺序。
  5. S2a 是**双写**：块流与旧 `Content`/aux 并存，读路径仍走旧格式，可整体回滚。写入点：
     `persistUserTurnExchange`（user 块）、`SendMessage` 轮末（助手块）、
     `persistCommandTurn`（命令块）、`attachBackgroundSessionUpdates`（子会话块）。
     块写失败不拖垮本轮（正文已由 `AddExchangeForAgent` 落过盘），只记日志。
  6. **C2** 服务端 mint 回合身份：`newMindFSID(agentID, seed)` 拼 UUID（不引 `google/uuid`，
     与 `randomHex` 同一条 `crypto/rand` 路径）。mint 点只有 `persistUserTurnExchange`
     一处 —— 三条服务端发起的轮次路径（`scheduled/tasks.go` 定时任务、`appcontext.go`
     看板任务阶段、子会话）都经 `SendMessage` 走到它。
  7. **C1** repoint 清 native id：`Manager.ClearNativeIdentity` 行级 JSON 手术删掉
     `native_turn`/`native_prompt`/`native_id`（**不是**结构体往返 —— 主日志里可能有别的
     写入者 / 未来版本加的字段，反序列化再序列化会把它们丢掉）。文件里本来没有 native id 时
     **一个字节都不写**（无谓重写会改 mtime、白白让缓存失效，而落地前的全部历史数据都没有
     native id）。清完删掉内存缓存与读盘游标：新文件更短时旧 offset 越过 EOF，增量读拿不到
     东西，带旧 native id 的那份缓存会一直留着。
  8. fork 复制历史时块流一起复制、seq 按 `seqMap` 重映射、`native_id` 清空；`turn_id`
     原样带过去（fork 复制的是同一批轮次）、`turn_seq` 重映射。fork 开的是全新 agent 会话
     （`AgentSessionID:""` + `ForkPoint`），源 native id 一律无效。
  9. 删会话时连块流一起删（否则正文留在盘上，会话 key 复用时被当成「本来就有正文」）。
  10. **S2b 读路径改走块流**（`GetBlocksWindow`，`blocks.go`）：窗口读**反向分块**读尾部
      —— 与 S1 的 `readAuxWindowTail` 同思路，但**不复用它**。那个实现丢掉每块的首个
      不完整行，跨越 512KB 边界的记录会静默丢失；块流一行可达 700KB（实测 os 单块
      119714 字符），必须两阶段：先反向探测出停止偏移（只解析块内部的完整行），
      再 `alignLineStart` 对齐到行首后**一次正向读到 EOF**。代价是 2× IO，换来的是
      「窗口里永远不丢正文」。窗口读必须是 O(窗口)：`m.mu` 每 root 一把，
      「每轮都全量读一遍块流」会让长会话打开时把所有请求串起来。
  11. `resolveBlocks` 按 `(seq,pos)` 取最高 `revision`、跳过 `step<0` 的未收口块、按
      `(seq,pos)` 排序。`CompactBlockLight` **只压工具卡细节**（复用 `CompactToolCall` +
      `lightCompactToolCall`），正文与 reasoning **一个字都不动** —— S1 的压缩是
      「工具卡按 seq 惰性取」，正文要是被压掉，这个方案就白做了。
  12. `sessionResponse` 回包内联 `blocks`（`http.go`），只带窗口内 seq（`windowSeqsForExchanges`）；
      与 `exchange_aux` 同一形状 `{seq: [...]}`。内联而不是让前端再发一次请求：窗口回包
      本来就是这一屏的**唯一**权威快照，多一次往返就多一个「正文还没到」的空窗。
  13. **前端按 `type` 渲染块**（`web/src/hooks/sessionBlocks.ts`，新增；`useSessionStream.ts`
      与 `SessionViewer.tsx` 接线）：`reasoning → thought`（灰显/可折叠，复用既有渲染）、
      `text → assistant_text`、`tool_call → tool`、`todo/plan/compact` 各自成项，
      未知类型**跳过而不是抛错**（agent 加新类型时旧前端必须能活）。
      顺序**只按 `pos` 遍历** —— 旧渲染器的 `lines`/`totalLines`/`emittedLines`/clamp
      全部消失（那套是「把 aux 按 `line` 插进 content 字符串」的产物，块流下无意义）。
      项 id 用 `block:<seq>:<pos>`：`(seq,pos)` 在一个会话里唯一且**跨 revision 稳定**，
      同一块被补正时 React 与虚拟列表的测量不重建（旧 id 含内容哈希，流式期每 chunk 换一次）。
      判定逻辑单独成 `.ts` 模块是因为 `.tsx` 在 `node --test` 下导入不了
      （见 `web/tests/ts-module-hook.mjs` 的边界说明），住在 hook 里就只能靠源码文本断言。
  14. 块跟着 `exchange_aux` 走完**全部**缓存路径（`services/session.ts` + `App.tsx`）：
      `toPersistentSession` 只留还在 exchanges 里的 seq、`withSessionMeta` 与窗口锚定
      「按 seq 覆盖」、`cloneSession` 浅拷贝。漏掉任何一条都会让**首帧种子**退回旧格式
      —— 双写期看不出差别，S2d 删掉 `content` 之后就是「切会话正文闪一下」。
      覆盖而不是拼接：同一个 seq 的块集合是**完整快照**（后端已按 revision 归并），
      拼接会让旧 revision 与新 revision 并存、渲染成两段重复正文；而 aux 在同一 seq 下
      会陆续追加，所以 aux 必须拼接。两者形状相同、语义相反，是刻意的。
  15. **S2c 身份判重**（`exchangeAlreadyRecorded`）：写入侧按 native id 判重，顺序是
      「先身份、后内容」。有身份且库里对不上 → **一律落库**（那可能是一轮真新增的内容，
      丢掉才是 bug；方案 D10：永不猜）；有身份且匹配上 → 跳过；没有身份 → 退回
      「内容归一化 + ±5s」，并按会话记一条「判重还在猜」的日志（可观测的退化指标）。
      实时路径只把 agent 真正返回的原生消息 id 写入 `agent_id`；MindFS turn id 不冒充
      agent 身份。ACP 回显 prompt turn id 时，仅按 user 行 `mindfs_id` 精确桥接，规则见 G-BI。
  16. **两端取身份必须同一口径**：ACP 的行边界 = **消息边界**（规范原文「`messageId` 变化
      表示一条新消息开始」），实时路径取「最后一个带 `messageId` 的文本块」。合并规则
      不一致就永远对不上。claude importer 同理在合并后取最后一个 `uuid`。
      `messageId` 只认 UUID 形态（`isUUIDShape`）：ACP 规范要求两端都用 UUID，而
      MindFS 递出去的就是自己 mint 的 `turn_id`；别的形态一律当「没有身份」，
      宁可不判重也不能拿一个对不上的字符串当身份。
  17. ~~**投影身份化**（`session_projection.go`）~~ —— 投影与 `audit.go` 已于 2026-10-12
      （G-AB 退役）整体删除：身份收敛（G-BI）之后历史残留归零，它唯一的作用消失。
      读时按内容再猜一次只会把真实缺陷藏成看不见。它现在只剩一个作用：折叠 S2c 之前
      遗留的、双边都没有身份的历史行（直接删会让实测那 9 对重复立刻变成用户可见的
      重复正文，那是一次实打实的回退）。审计端点新增 `identity_bearing_rows` 报出
      「身份化进度」，也就是历史残留的规模 —— 那是删掉投影与处理历史行的时机判据。
  18. **S2d-1 回填**（`session_blocks_backfill.go`）：窗口里「一个块都没有」的 seq，
      用**调用方本来就传进来的** exchanges + aux 编译成块并落盘，幂等。读时惰性而不是
      写迁移脚本：会话库按项目/账户分区散在各处（本机就有 4 个 `.mindfs/sessions`，
      WSL 端另有一份），没有任何一处能保证跑全迁移脚本；而输入在读的时候就在内存里，
      不额外读盘才是关键 —— `GET /api/sessions/{key}` 是全站最重的端点（单次 1.14MB，
      客户端 1.7s 一次轮询，占 70% 流量）。实测：133 个会话 / 39517 条块，
      **救回 29 轮「正文已丢、思考还在」的轮次**，第二次跑 0 回填。
      投影与回填的交互当时已实测排除风险：被投影隐藏的重复行 **aux 全为空**（工具卡与
      reasoning 都在保留的实时行上），所以按各自的 content+aux 编译不会丢东西。
  19. `TimestampFloor` 只给 `HasTranscriptTimestamps` 的导入器（claude/codex）。ACP 的
      `SessionUpdate` 不带时间戳，它给每行盖的是「读到它的那一刻」，恒晚于地板 ——
      地板形同虚设，而代码看着像「有地板就安全」。删掉那道假防线后，ACP 侧的正确性
      由 native id 身份判重负责。
  20. **S2d-2 块流成为正文唯一载体（D1）**：主日志一行的 `content` 恒空，正文只写块流。
      三个写入点全部收紧 —— `persistUserTurnExchange`（user 行）、`SendMessage` 轮末
      （助手行）、`appendImportedExchange`（导入行）。字段保留为 `LegacyContent` 且
      **json tag 仍叫 `content`**：老会话文件必须还能解析，而它现在的唯一读者是
      S2d-1 的回填（`backfillTurnsFromWindow`）。助手块的写入从「失败只记日志」改成
      **返回 err**：主日志那一行只剩元数据，块丢了就等于这一轮没有正文，而用户看得出来；
      导入路径仍只记日志（那一行已经落盘，块失败不该让整行消失）。
  21. `addExchangeForAgentAt` 的空行守卫跟着改：S2d-2 之前它按「空正文 + 非 agent 角色」
      跳过，而 user 行现在**恒空**，照旧判据会把**每一行 user** 静默丢掉（轮次元数据
      `turn_id`/`native_prompt` 跟着没，前端按 seq 合并时那句话不显示，搜索的 user 命中
      优先也失效）。改成「什么都没带」才算空行：空正文 + 空 agent + 无任何轮次身份。
  22. 判重的内容比对改读块流（`recordedExchangeBody`）：claude 的 user 行拿不到 native
      uuid，判重只能靠正文，而正文现在住在块流里。不读就会把同一个 prompt 反复当新行
      写下去 —— 那正是「ask 下面又渲染一轮出现过的文字」。
  23. 搜索改两遍（`searchSessionContent`）：先搜块流（text + reasoning），再扫主日志。
      块流那遍是新内容的主路径，主日志那遍为了**还没回填的老会话**（回填是读时惰性的，
      会话没被读过就还没块）。省掉任一遍都是静默失效。
  24. fork 分工明确：`copyForkBlocks` 复制**源已有的块**（revision/step 是盘上真值，
      不重编号），`fillForkBlocksFromLegacy` 给老会话补编译。两者都要 —— 只补编译会丢
      源块里的 revision 信息，只复制会让老会话 fork 出一份没有正文的会话。
  25. `sessionSummary`（appcontext.go）与 `copyForkHistory` 的主日志正文读取全部改走
      `GetBlocksWindow` —— 摘要标题、fork 复制都从块流取，否则新会话的标题是空的。
  26. `Manager` 新增 `TurnIDForSeq`：块要带 `turn_id` 才能挂回轮次，而 `max+1` 分配下
      「刚写下的那一行」的 seq 只有调用方算得出、`turn_id` 却存在行里（导入路径就是
      拿到 seq 之后回过头来问这一行的身份）。
  27. **S2e pending 身份改用 `turn_id`**：`SessionPendingState.TurnGen uint64` →
      `TurnID string`，`SetPendingUser/At`、`SetPendingReply`、`ClearSessionPending`、
      `PendingTurnGenMatches`→`PendingTurnIDMatches`、`AppContext.EndSessionTurn`、
      `scheduled.SessionActivityBroadcaster` 三个方法同步换。语义不变：空串 `""` 对位
      旧的 `0`，仍是「无身份 → 保守清」的哨兵。**不是**把两套身份合并成一个 ——
      pending 灯在轮次**落盘之前**就要有身份（`OnStart` 那一刻主日志行还没写），
      而 `turn_id` 是在 `persistUserTurnExchange` 里才 mint 的，两者时刻不同。
      mint 点收敛到 `session.NewTurnID()`（原来在 usecase，pending 也要 mint，两份 mint
      迟早分叉），`usecase.newTurnID` 留包内转发。形状判据同样收敛：
      `session.IsTurnIDShape` 一份（ACP 的 `isUUIDShape` 改成转调它，不再有第二份拷贝）。
      连带补齐 `AppContext.RunAgentStage` 的回合身份 —— 它与 tasks.go 同形状，G-BE
      当年只扫了 tasks.go，于是这条路径一直是「置 pending 拿不到身份、收尾按无身份
      保守清」（灯不灭被兜住，但新轮的灯会被误抹）。
  28. **agent 分析块挂到前一条 user 行**：调用方从可能陈旧的 `Session` 快照预测
      `max(seq)+1`，Manager 在锁内却基于更新后的会话分配了另一 seq；aux 与块流随后
      写到预测 seq。重开会话后，穿插在 agent 回复里的 reasoning/工具信息就会出现在
      user 消息上。Manager 必须返回实际分配的 seq，调用方以它写 aux 和块流。
- 针对性测试：
  - `server/internal/session/manager_blocks_test.go` → 块追加/读回顺序、缺文件为空、
    按 seq 过滤、容忍无 `pos` 的旧行、块流与主日志同目录、删会话连块流一起删。
  - `server/internal/session/blocks_build_test.go` → **只有 reasoning 的轮次仍产出块**
    （本 bug 的核心回归）、空轮次不产块、按 `line` 交错且空正文段跳过、同 `line` 保持到达序、
    各类 aux 的 type 判定。
  - `server/internal/session/manager_identity_fields_test.go` → 身份字段 JSONL 往返、
    无身份历史行读成零值、块与主日志共用同一个 `turn_id`。
  - `server/internal/session/manager_native_identity_test.go` → 清 native id 同时清主日志与
    块流、真的落盘（换 manager 直读）、干净文件一个字节不动、未知字段不丢、`native_id` 往返。
  - `server/internal/api/usecase/session_blocks_write_test.go` → `newMindFSID` 是 UUID 且不重复、
    user 行拿到 `turn_id`+`turn_seq` 与 user 块、判重命中不重复写块、**正文为空的轮次留下
    reasoning 块**、块序与渲染器交错一致、陈旧会话快照仍把 reasoning 块写到 Manager 实际
    分配的 assistant seq。
  - `server/internal/api/usecase/session_fork_blocks_test.go` → fork 复制块流并重映射 seq、
    不带源 native id、`turn_id` 跟着复制。
  - `server/internal/api/usecase/session_early_user_persist_test.go`（既有）→
    早落盘契约与判重对称性在签名加返回值后仍成立。
  - `server/internal/session/manager_blocks_window_test.go`（S2b）→ 窗口读**只读尾部**
    （`fullBlockReads == 0`，300 个 seq × 4 块）、单行大于一个读块时不丢（700KB）、
    按 revision 取最高且跳过 `step<0`、空输入、窗口外 seq 不泄漏、坏行跳过、
    `CompactBlockLight` 保正文只丢工具细节、未知类型不被压。
  - `server/internal/api/http_window_test.go`（S2b）→ 窗口回包内联 `blocks` 且只含窗口内 seq、
    没有块流的历史会话拿到空 `blocks`（前端靠它回退）、**块的线上字段名精确契约**
    （Go 的 json tag 与手写的 TS `SessionBlock` 没有编译期联系，改一个 tag 就能让前端
    静默读到 `undefined`、正文又变空，而两端单测全绿）。
  - `web/tests/session-blocks-render.test.mjs`（S2b）→ **只有 reasoning 的轮次必须渲染出内容**
    （本 bug 的核心回归）、按 `pos` 交错保序、各 `type` 映射、空块不产空气泡、
    未知类型跳过、`context_window` 贴最后一个 `assistant_text`、块 id 跨 revision 稳定、
    `blocksForSeq` 把空数组当「没有块流」。
  - `web/tests/tool-card-grouping.test.mjs`（既有，S2b 改）→ 它的手搓 CJS 加载器要多认一个
    `./sessionBlocks` 依赖，且**递归加载真模块而不是桩掉**（桩错了会让分组测试变成
    「测了一个假模块」）。
  - `web/tests/session-window.test.mjs`（既有，S2b 改）→ `toPersistentSession` 的返回字面量
    多了 `blocks`；判据仍是「没淘汰 ⇒ 显式 `truncated: false` 且 aux/块都被重新赋值过」。
  - `server/internal/api/usecase/session_identity_dedup_test.go`（S2c）→ 身份优先判重的
    8 条单测（内容相同但身份不同 / 身份不同就不折叠 / 有身份就不退回内容 / 空内容不参与
    内容判重）+ **端到端三条**：live-owned 会话 Full 同步后库行数不变（**临时把身份判重
    短路掉这条会红，且红出来的形态与实测一致：4 行而不是 2 行、时间戳差两小时、
    `前半段` vs `前半段\n\n后半段`**）、连同步三次仍不变、身份没匹配上的行**必须落库**
    并记住自己的 `native_turn`（否则下次同步认不出自己）。
  - `server/internal/session/types_aux_reasoning_test.go`（S2d-1）→ **压缩不许碰
    reasoning**：只有 reasoning 的 aux 不被整条丢弃、与 toolcall 并存时不被清空、
    Light 变体同样保留；同时反向钉住「压缩该做的事仍然要做」（清掉 edit/read 的
    content），免得日后有人把修复顺手「修」成不压缩。
  - `server/internal/api/usecase/session_blocks_backfill_test.go`（S2d-1）→ 回填后块的
    正文与老主日志**逐字相同**、幂等（第二次跑块流文件一个字节都不变）、**正文为空但
    aux 有 reasoning 的轮次被救回**、已有块的轮次一个字节都不动、什么都编译不出来的
    会话不建空块流、`missingBlockSeqs` 与读路径同口径、seq=0 不参与。
  - `server/internal/api/usecase/session_content_legacy_test.go`（S2d-2）→ 四条降级契约：
    live 轮写完后主日志一行的正文必须是空的（**且块流里找得到**，否则就是真丢了）、
    导入行同样只写块、user 判重改读块流（同一句话相邻两次仍只落一行）、真人间隔十分钟
    的重复提问照旧落三行、fork 老会话仍把正文带过去。
  - `server/internal/session/user_turn_survives_test.go`（S2d-2）→ **空守卫那条改动的
    回归**：空正文的 user 行必须落得下去（改之前整行被吞、前端看不到自己说过的话），
    空正文的 agent 行同样不被误吞。
  - `server/internal/session/search_blocks_test.go`（S2d-2）→ 搜索两遍都不能省：
    块流命中（新内容）、块流没有时退回主日志（还没回填的老会话）、两边都搜不到就是没命中。
  - `server/internal/session/manager_duplicate_seq_test.go`（S2d-2 改字段名）→ 同 seq
    重复行的合并不受「主日志不再有正文」影响（判据对新写入的行是恒等的 no-op）。
  - `server/internal/api/http_window_test.go`（S2b 改，契约变了）→
    `TestHandleSessionGetWithoutBlocksReturnsEmpty` 的断言**整个反转**成
    `TestHandleSessionGetBackfillsLegacyBlocks`（老正文必须在**同一个回包里**出现 ——
    回填完再让前端自己拉一次，用户看到的就是先空一屏、再突然冒出正文）；
    另加「块真的落盘了」（换 manager 直读盘，只断言响应不足以证明落盘 —— 那样只是
    把猜测从读时搬到读时）与「真的什么都没有的会话仍返回空 map」。
  - `server/internal/api/stream_hub_turn_identity_test.go`（S2e，**取代**已删除的
    `stream_hub_pending_identity_test.go`）→ 身份是 UUID 形状、同会话两轮身份不同、
    迟到 done 不清新轮、重复 done 幂等、空串仍保守清、用户路径同样可用、子会话由父轮
    终结、`PendingTurnIDMatches` 迟到不放行/空身份放行/条目不存在放行。这七条就是旧文件
    那七条的逐条对译 —— 换表示时语义必须一条不变。
  - `server/internal/api/kanban_turn_identity_guard_test.go`（S2e）→ **看板阶段路径的
    源码守卫**（此前只有 tasks.go 有，这条路径裸奔）：置 pending 的返回值必须被接收、
    收尾不得传哨兵身份（`0` 与 `""` 都拦）、不得再出现无身份的 `BroadcastSessionDone`。
    已用「把修复改回原样」验证三条都真的会红。
  - `server/internal/scheduled/pending_identity_test.go`（S2e 改）→ 守卫 3b 补拦**空串**
    哨兵 —— 原来只拦字面 `0`，换表示后那道正则成了摆设（实测：`..., "")` 原样放过）。
  - `server/internal/agent/acp/importer_identity_test.go`（S2c）→ `messageId` 带成 `NativeID`、
    重放两次身份稳定、`messageId` 变化开新行、无 `messageId` 仍合并相邻块、非 UUID 被拒、
    时间戳打 `SynthesizedAt` 标记、合并不改写时间戳、`AgentThoughtChunk` 导入成 think 卡、
    同一 `messageId` 的 thought 与文本合成一行。

### G-BI 回合身份收敛为两个 id（turn_id/native_* → mindfs_id/agent_id）

- 来源：2026-10-12 main 的回合身份收敛。
- 可见症状：同一轮在同一条读路径上有三种身份表示（主日志 `turn_id`、pending 灯 `TurnGen`、
  块流 `native_id`），四种 legacy 键名在不同入口各写一两种，于是「同一回合的两次写入」
  只能用内容相似度 + 时间窗猜。用户回声与助手回复的去重漏判/误判，实时流与读取结果对不上账。
- 边界：全系统只剩两类 id —— **`mindfs_id`**（MindFS 铸、落盘即定、永不改、系统内唯一）与
  **`agent_id`**（agent 侧桥梁键，可空可后补，不参与唯一性）。无 agent id 时由
  `NewMindFSID(agentID, seed)` 确定性派生 UUIDv5，判重因此可复现。
- 为什么必须保留：`Exchange` / `Block` 各加 `UnmarshalJSON` 兼容层，把老 JSONL 里的
  `turn_id` / `native_turn` / `native_prompt` / `native_id` 逐条映射到新字段。没有这层，
  133 个存量会话读出来身份全空、已回填的 39517 个块与轮次脱钩，用户历史正文当场消失。
  前端 overlay 改为按（角色类别、`mindfs_id`）对账，**显式不按正文相似度** ——
  相似度对「同一个 user prompt 出现两次」无能为力，而那正是它想拦的重复；
  无身份的帧保留而不是猜归属。`latestSeq` 水位线随之删除。
- 针对性测试：
  - `server/internal/session/manager_rename_compat_test.go` → 老键名的 exchange / block 行
    必须还能读出身份、`mindfs_id` 与 `agent_id` 各归其位；
    `rename_compat_helpers_test.go` 供它写原始 JSONL 行。
  - `web/tests/session-overlay-unit.test.mjs` → 同 `mindfs_id` 的用户回声与助手回复必须合并、
    不同 id 不得按相似度合并、无身份帧保留。
  - `web/tests/session-window.test.mjs` → `SessionViewer` 不得再引用 `latestSeqState` /
    `noteLatestSeq` / `latestSeq*`（水位线退役）。

### G-BJ 会话列表按实时活动时间排序 + 退役多项目列表预渲染缓存

- 来源：2026-10-12 main 的活动时间排序与缓存退役。
- 可见症状：列表排序键是 `updated_at`，服务端只在回合收尾推进它，于是**回复还在流的时候**
  会话停在原地、回答完了才跳一下，切走再回来顺序又回去。多项目列表另有一层 IndexedDB
  预渲染按账户存整份快照：账户切换或首次打开会先画**上一次的结果**再被节点响应重排 ——
  用户看到列表「闪一下又变」。
- 边界：新增**客户端专属**覆盖层 `activity_at`（不落盘）：流事件到达就盖到会话与项目分组上，
  100ms 粒度；合并只认更新的覆盖层，一旦持久化 `updated_at` 追上就自动删除。项目分组改按
  **节点表顺序分区**（未配置节点排最后且顺序稳定）。多项目预渲染与
  `multiRootSessionListCacheKey` 退役；单项目列表缓存仍按账户隔离。
  C1 补色、C2 逐组合并、`_unreachable` 三条定制全部保留。
- 为什么必须保留：覆盖层必须插在「节点归属过滤」**之前** —— 那条过滤会把发往别的节点的帧
  丢掉，而活动时间要在任何重排之前先推进。`activity_at` 只在新值 ≥ 旧值时才写入，
  绝不把活动时间往回拨。
- 针对性测试：
  - `web/tests/session-list-merge.test.mjs` → 活动时间优先于 `updated_at`、置顶区与普通区
    各自独立、陈旧刷新不得把活动时间拨回、`updated_at` 追上后覆盖层清除、
    `orderProjectGroupsByNode` 的节点分区与未知节点稳定性、流处理器里 `activity_at`
    必须在节点归属过滤之前。
  - `web/tests/multi-account-partition.test.mjs` / `web/tests/upstream-restore.test.mjs` →
    多项目预渲染的缓存 API 与键必须**不存在**（退役被改回来时立刻红）。

### G-BK 发送前落盘所选模型（回合内模型切换 + 内部别名拒收）

- 来源：2026-10-12 main。
- 可见症状：模型是 set 会话元数据而不是回合的**结果**。原先它只在 `OnStart` 之后才被
  reconciled 回写：流式期间任意一次列表/窗口刷新都会拿回旧模型，用户正在打的这轮用的是
  新模型、页面显示旧模型，回合一结束又跳成新模型。另一侧：agent 的内部别名（如 `sonnet` /
  `opus` 这类聚合假名）会被当成合法 model 写进去，运行时才发现不支持，而此时 user 行
  **已经落盘** —— 留下一条用户没等到任何回复的孤立消息。
- 边界：选中的模型在 user 行落盘与 `OnStart` **之前** `UpdateModel` 落库；运行时实际模型
  仍可能被 agent 规范化，那在本轮收尾时按 LiveModel 收敛，不是这里的职责。
- 为什么必须保留：不支持模型的校验必须**抢在 user 行落盘之前** —— 本轮压根不会开始，
  user 行也就没有存在过。顺序反了就是孤立消息。内部别名只在后端拒收（前端仍可展示成组
  模型名，见 G-R）；拒绝边界放 `validateAgentModel`，与 G-R 的 `[1m]` 兼容判定同一条路径。
- 针对性测试：
  - `server/internal/api/usecase/session_model_switch_test.go` → 选中的模型必须在本轮开始前
    落库（流式期刷新拿得到）、运行时活模型优先于持久化快照。

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

### G-BL 看板新建任务的 worktree 节点路由（2026-10-12）

- **来源**：修正工作台快速新建任务将项目节点丢弃的问题（原入口由 `603bfc9` 接入，`0792221` 改为直接打开共享新建面板）。
- **可见症状**：从工作台为远端 Git 项目新建任务时，worktree 开关和分支选择器消失；同名项目还可能按另一节点的 Git 状态判断，并把分支请求或任务创建发到错误节点。项目看板初始加载也可能用同名项目的回退记录判断 Git 状态。
- **根因与边界**：`rootId` 在多节点间不是唯一身份。旧工作台回调虽收到 `nodeId`，却只把 `rootId` 交给新建面板；面板再通过 `byId` 的首个同名项目判断 `is_git_repo`。现在以 `nodeId::rootId` 贯穿项目选择、Git 仓库判断、分支拉取、偏好存储、附件上传、任务创建和新建 worktree 刷新。显式节点没有匹配元数据时不回退到另一节点的同名项目。
- **针对性测试**：
  - `web/tests/task-worktree-node-scope.test.mjs` → 两节点同名项目必须命中所选节点；缺失所选节点记录时不得退回另一个节点；项目作用域键可往返解析；工作台入口、worktree 判断/偏好与任务提交保留目标节点。
  - `web/tests/task-board-view.test.mjs`、`web/tests/workspace-board.test.mjs`、`web/tests/node-colors-and-workspace-preferences.test.mjs` → 共享任务面板接收并使用节点 ID，项目下拉/颜色和 worktree 分支请求都按节点区分。
- **边界**：仅看板新建任务的项目身份路由与 worktree 选择/创建链路。已有任务的执行、收尾和 worktree 生命周期策略不变。

### G-BN 多 agent 会话按指定 Claude binding repoint

- **可见症状**：同一个 MindFS 会话先后使用 Claude 和 Codex 时，Claude 转录仍在已删除的 worktree slug；按 Claude `agent_session_id` repoint 却报“only supported for codex”，会话无法按收尾流程搬回主 checkout。
- **根因与边界**：`InferAgentFromSession` 返回最近写入 exchange 的 agent，不能代表脚本明确指定的 Claude 转录 binding。传入 `agent_session_id` 时先按 Claude binding 反查目标会话，校验显式 key 与 binding 同一，再只关闭 Claude 运行时、搬转录并在事务中更换该 binding 和末尾游标，然后持久化清除会话 worktree 归属；Codex binding 保持原样。
- **针对性测试**：`server/internal/api/usecase/session_repoint_test.go` → 多 binding 下显式 Claude id 不被最近的 Codex exchange 覆盖；错误 key/id 组合被拒绝；临时转录端到端搬移确认新 binding 的游标指向 EOF、`agent_ctx_seq` 保留、Codex binding 不变。
- **边界**：当调用方明确提供 Claude `agent_session_id` 时选择对应 Claude binding；仅传 session key 的调用仍根据当前 agent 判定，并拒绝不支持的 provider。Codex 自身转录格式的 repoint 不在本组范围。

### G-BM Agent 中立的规则入口与计划索引

- **可见症状**：Codex、Claude Code、Gemini CLI 等 agent 可能加载不同入口，遗漏规范或依照过期计划执行；规则副本漂移后会产生相互矛盾的要求。
- **根因与边界**：以 `AGENTS.md` 为共享操作/架构入口，`docs/agent/rules/` 为代码规范正文，工具入口只导入共用文件；`.claude/` 继续承载 Claude 专属 skills/runtime 配置。检查并修正上游合并 skill 中与仓库节点表冲突的 PATH 注入和本机 systemd 用户单元说明。`docs/plans/README.md` 是唯一计划状态索引，历史计划不再隐式充当待办。
- **针对性测试**：`web/tests/agent-docs.test.mjs` → Claude/Gemini 入口导入共用规范与索引、规则和计划索引中的相对链接可达、每份计划都在唯一索引分类。
- **边界**：规则文档的唯一来源与 agent 加载入口、计划索引维护。具体代码规范内容仍由对应规则文件负责。

### G-BO taskIcons 导出职责清单

- **可见症状**：`taskIcons.tsx` 同时承载图标、任务样式和颜色工具，新增导出时难以看出模块范围是否扩大。
- **根因与边界**：文件头列出所有导出并按职责分组，针对性测试将清单与真实导出对照。审计调用方后将任务呈现实现归入 `components/task`；`app/taskIcons.ts` 保留兼容 facade，导出行为不变。
- **针对性测试**：`web/tests/task-icons-inventory.test.mjs` → 文件头清单必须精确覆盖实际导出的函数与类型。
- **边界**：只记录并验证模块职责，不改变图标、样式和颜色工具的实现。

### G-BP 前端 update 与 agent service 域归位

- **可见症状**：更新和 agent 服务实现散落在 `services/` 根目录，不符合按域收拢规则；物理搬迁若漏改映射，还会让源码守卫测试失效。
- **根因与边界**：实现分别归入 `services/update/` 与 `services/agent/`，原根路径保留纯 re-export 门面。既有消费者继续使用旧公开路径，源码逻辑路径映射同时指向 facade 与实现。
- **针对性测试**：`web/tests/service-domain-facades.test.mjs` → 7 个根入口都只 re-export 到存在的域实现。
- **边界**：仅调整文件归属及实现内相对路径，不改变服务 API 或行为。

### G-BQ 前端异步三态消息复用

- **可见症状**：项目目录浏览、worktree 和定时任务列表的异步状态文案由各自 JSX 手写，加载和错误状态的可访问语义不一致，样式调整也需要逐处维护。
- **根因与边界**：`components/common/AsyncStateMessage` 负责统一状态消息语义，调用方仍通过样式覆盖保留原有尺寸/布局，并继续自己决定 loading、error、empty 的优先级。测试锁住语义和三个调用点。
- **针对性测试**：`web/tests/async-state-message.test.mjs` → loading 使用 status、error 使用 alert、空态不伪装成错误，组件被三类列表复用。
- **边界**：只整理状态消息，不改变数据获取、列表呈现或页面排版。

### G-BR 前端 JSX helper 文件后缀与命名门禁

- **可见症状**：`sessionCardUtils.tsx` 使用 `Utils` 名称却包含 JSX，命名测试未扫描 `.tsx` 版本，文件名无法说明其实际职责。
- **根因与边界**：改名为 `sessionCardHelpers.tsx` 并更新三个消费者；命名门禁检查 `*Utils.tsx` 中是否仍有 JSX。导出和渲染行为不变。
- **针对性测试**：`web/tests/naming-convention.test.mjs` → 含 JSX 的 `*Utils.tsx` 必须改用 `*Helpers.tsx`。
- **边界**：只统一该存量文件的后缀语义，不重构其导出内容。

### G-BS 前端组件域类型文件名归一

- **可见症状**：Action 组件域类型文件叫通用的 `types.ts`，不能从路径分辨其类型归属，命名门禁也没有检查组件域裸 `types.ts`。
- **根因与边界**：改为 `actionTypes.ts` 并更新 9 个同域消费者；命名测试阻止 `components/<domain>/types.ts` 回归。服务域内 `types.ts` 与 i18n 的公共类型入口仍按既定目录规则保留。
- **针对性测试**：`web/tests/naming-convention.test.mjs` → 组件域类型文件必须使用域名后缀。
- **边界**：只改文件名和类型导入，不改类型结构与行为。

### G-BT composer 样式与图标职责分离

- **可见症状**：`composerStyles.tsx` 同时导出样式值和图标 JSX，样式文件被迫使用 TSX 后缀，调用方也从样式模块导入图标。
- **根因与边界**：样式实现移入纯 TypeScript `composerStyles.ts`，六个 JSX 图标移入 `composerIcons.tsx`；调用方按用途分别导入，旧源码守卫逻辑路径映射到两份实现。
- **针对性测试**：`web/tests/naming-convention.test.mjs` → 样式模块只能用 `*Styles.ts`；`web/tests/task-stage-panel.test.mjs` → TaskDetailPanel 图标来自图标模块、原共享样式约束仍在。
- **边界**：只分离职责，不改变样式值、SVG 或 UI 行为。


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
  - **拆分/搬移代码时必须同步改 `anchors:`**（2026-10-11 补）。锚点是「文件路径 + 符号子串」，
    而前端经历了三轮大拆分（`App.tsx` 15k 行 → 16 行分层外壳、`services/session.ts` 2741 行 →
    501 行 facade、`ScheduledAgentTaskDialog` 的删除确认搬进 `useScheduledAgentTaskDialog`）。
    符号还在，但它**搬到了另一个文件**，门禁就以「锚点失配」报警 —— 这不是假警报，
    是「这个符号的保护坐标过期了」。核对方法：`make check-upstream` 报哪条失配，
    就 `grep -rn` 该符号在 `web/src/` 里的真实位置，改锚点路径。
    本次一次修了 11 条（G-F/G-Y/G-AJ/G-BC/G-AY×4/G-BD/G-BH），全部是这种搬迁，没有一条是定制丢失。
    **判据**：若符号在全仓都找不到，才是定制真丢了，要按组内 `why` 恢复；找得到 = 改锚点。
    `clearStalePending`（G-AY）是唯一例外 —— 该组 `boundary` 自己记着「第一版修法是打补丁，
    已被纯派生改造废除」，所以锚点是跟着定制一起退休，删锚点而不是搬。

### 7.1 锚点是抽样标记，不是覆盖率（别把它读成「已检查 9%」）

2026-10-06 把 `CUSTOM(` 全量数了一遍，三件事必须分清 —— 混起来会得出「覆盖了 90%」这种错结论：

| 层 | 是什么 | 现在有多少 | 由谁强制 |
|---|---|---|---|
| `files:`（yaml） | 「这段代码是我们的」 | 565 条登记，覆盖全部 **837** 个 delta 文件 | 门禁断言 1（每个 delta 文件必须被某组认领）+ 断言 2（登了却不在 delta 里 = 已被上游吸收） |
| `anchors:`（yaml） | 「这个符号必须还在」 | **214** 条，分布 **35** 组 | 门禁断言 4（子串必须在源文件里命中） |
| `CUSTOM(G-xx)`（源码内） | 「**上游同点**改这里会冲突」 | **61** 处，落在 **38** 个非测试文件 | 无（人读） |

- 第三行才是「锚点」的字面意思：它**只加在上游已存在的文件里的定制 hunk 上**。纯新增的 fork 文件（504 个 A）永远不带标记 ——
  合上游时它们整份冲突，标记不提供任何额外信息。所以 **`CUSTOM(` 的占比天然不可能高，也不该高**。
- 它是**抽样标记**：61 处覆盖不了 111 个 M 文件里的每一处定制 hunk，**没标记 ≠ 没定制**。
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


---

## 3.2 补全分组（2026-10-11 新增）

### G-BI 会话身份收敛为两套 id（mindfs_id + agent_id）（2026-10-11，2026-10-12 延伸）

- 来源：`server/internal/session/types.go`、`server/internal/session/manager.go`、
  `server/internal/session/blocks.go`、`server/internal/api/usecase/session.go`、
  `server/internal/api/usecase/external_sessions.go`、`server/internal/api/stream_hub.go`、
  `server/internal/agent/claude/session.go`（2026-10-11）。
  前身是 G-BH 的 S2c/S2e，本组把「turn_id + native_turn + native_prompt + native_id +
  TurnGen」五个名目合成两个。

  - 2026-10-12 UI 延伸：来源还包括 server/internal/api/stream_hub.go、ws.go、appcontext.go，以及 web/src/services/session.ts、web/src/app/appSession.ts、web/src/app/useSessionStreamCache.ts、web/src/app/useRealtimeEvents.ts、web/src/shared/sessionOverlay.ts、web/src/components/session/SessionViewer.tsx。session.user_message 与 session.stream 都携带本轮 mindfs_id；瞬时缓存保留无正 seq 的实时项，但只在角色类别和 mindfs_id 同时匹配时让位给持久行。相同正文、不同身份继续显示；无 id 的瞬时项继续显示。
  - 2026-10-12 身份写入延伸：user `mindfs_id` 在 Manager 锁内按真实分配的 seq 铸造，并由 `MessageStart` 原样传给 pending 与用户回声；ACP 收到的是 MindFS turn id，导入回显时只作为 user 行桥梁比较，不写成 `agent_id`。删除 `UsesPromptMessageID` 能力分支，避免把「是否有原生消息 id」与「是否收到 MindFS turn id」混成一种身份。

  - **边界**：会话轮次元数据的**字段名与铸造规则** —— `Exchange.AgentID`、
  `Block.AgentID`、`NewMindFSID`、`exchangeAgentKey` / `exchangeIdentityRecorded`、
  `stream_hub` 待亮灯身份。G-BI 提供写入侧身份判重；读时内容投影已在 2026-10-12 由
  G-AB 退役，`exchangeAlreadyRecorded` 仍保留以保证重复导入幂等。

- 可见症状（没有它会怎样）：
  1. **系统里有五套回合身份**：MindFS 铸的 `turn_id`、claude 写的 `native_turn`、
     ACP 写的 `native_prompt`、块流的 `native_id`、pending 灯另算一套 `TurnGen`。
     「本轮是哪个回合」这个问题在五处各有一个答案、各有一套生成规则 ——
     正是 `message-mechanisms-plan.md` §12 警告的「两套回合身份真值并存必然打架」的五倍版。
  2. **导入路径根本不铸身份**：live 的四个写入点都铸了 `turn_id`，
     只有 `appendImportedExchange` 没有。于是 Full 同步重放出来的行与实时行
     **两层判重同时失效** —— 没有 id 可比，而「内容相似 + ±5 秒」在导入把整段历史
     重读一遍的场景下必然失效（实测 9 对重复行，而会话库里一共才 53 个会话）。
  3. **live 这一侧拿不到 agent id**：claude 的 `handleAssistantMessage` 读了 `msg.UUID`
     但从没带出去，所以只有导入侧有 agent 侧身份 —— 两侧永远对不上，
     判重永远只能退回内容比对。
  4. **pending 灯的身份与落盘那一行不是一个 seed**：`SetPendingUserAt` 曾从
     `baseExchangeSeq` 独立铸 id，落盘行使用 `userExchangeSeq`，两个值在同步/陈旧缓存时
     可以不同。现在 `persistUserTurnExchange` 返回落盘行的 canonical `mindfs_id`，pending
     和用户回声只复用这个值，不再各自铸造。
  5. **`nativePrompt = turnID` 的自指假身份**：`persistUserTurnExchange` 里把 MindFS
     自己铸的 id 又写进 agent 侧字段，让「有身份」变成一个恒真的假信号。

- 为什么必须保留：
  1. **全系统只留两类 id**。`mindfs_id` = MindFS 在写入点统一铸、系统内唯一、不变、
     实际使用的那个；`agent_id` = agent 返回的（claude 转录 uuid / ACP messageId），
     格式随 agent 而异，只作为把两侧行对上的桥梁。
  2. **`NewMindFSID(agentID, fallbackSeed)` 是唯一铸造点**：UUIDv5
     （固定 namespace + `\0` + seed 的 SHA-1 前 16 字节，不引 `google/uuid`）。
     确定性是关键 —— seed 优先取 `agent_id`，于是同一轮的 live 与 import 两侧
     **铸出同一个 id**，导入侧不再需要伪造 `turn_seq` 去对齐。
     `agent_id` 为空时退到 `sessionKey|role|seq`，两样都为空则返回空字符串
     （调用方的 `MindFSID == "" && AgentID == ""` 守卫兜住）。
  3. **`NativeTurn` / `NativePrompt` / `NativeID` 三个字段合并成一个 `AgentID`**：
     它们本来就是「agent 侧回查键」这一个概念，拆三个只会让判重口径分叉 ——
     claude 写左边那个、ACP 写右边那个，同一件事两套写法，改判重顺序时要同时记得
     两处。合并后 `IsMindFSIDShape` 也是唯一形状判据。
  4. **改名必须留读路径**：`Exchange.UnmarshalJSON` 与 `Block.UnmarshalJSON`
     各有一个 alias 结构体，把 `turn_id` / `native_turn` / `native_prompt` /
     `native_id` 四个旧键映射到新字段，同名字段优先。鍵名一旦写进盘（320 个会话、
     3595 行），改名不留读路径就等于把它们全部读成「没有身份」。
     用 alias 结构体而不是 `json.RawMessage` 散布各字段：一处读完、一处映射。
  5. **claude 侧补上 `MessageID`**：`subagentMeta.MessageID = msg.UUID`，
     `emitMessageChunkWithMeta` 把它带进 `types.MessageChunk`。这是「两侧能对上」
     的另一半 —— 只有导入侧有 id，确定性铸造也无从对齐。
  6. **待亮灯身份的 seed 与落盘那一行同源**：`SetPendingUserAt` 改成收 `userExchangeSeq`
     并拼 `sessionKey|user|%08d`。这不是「顺手统一」，是蓝灯能不能灭的问题。
  7. **`IsTurnIDShape` → `IsMindFSIDShape`**：名字跟着字段走，
     避免「Shape 判的是 turn 还是 MindFS id」这种再次分叉。
  8. **ACP 的回显值不等于 agent 原生身份**：`PromptRequest.MessageId` 收到的是
     MindFS 传入的 turn id；若 importer 把它写进 `agent_id`，就把同一个 MindFS 值伪装成
     agent id。现在只对 user 行做精确桥接比较，真正的 agent 身份只记录协议返回的原生 id。

- 针对性测试：
  - `server/internal/session/manager_rename_compat_test.go` +
    `rename_compat_helpers_test.go` → **钉住兼容层**：旧键 `turn_id`/`native_turn`/
    `native_prompt`/`native_id` 能读进新字段、**同名字段出现时新键赢**、
    Block 走同一条规则。这是本组最初为兼容层新增的测试文件 —— 改名类改动的全部风险都在这里，
    而它是纯读路径，不加测试就没人知道存量会话读出来是什么。
  - `server/internal/session/manager_identity_fields_test.go` / `manager_native_identity_test.go`
    → 身份字段的读写与清理（`ClearNativeIdentity` 现在清 `agent_id`）。
  - `server/internal/api/usecase/session_identity_dedup_test.go` → 判重两层顺序
    （先 `agent_id` 后 `mindfs_id`、都没有才退回内容），以及用户/agent 两侧
    各自带自己的 id（`liveUserCtx` / `liveAgentCtx` 分开）。
  - `server/internal/api/stream_hub_turn_identity_test.go` → 待亮灯身份 = 落盘那一行
    的 `mindfs_id`（改了 seed 就会红）。
  - `server/internal/agent/acp/importer_identity_test.go` → 导入行也带 `agent_id`。
  - `server/internal/api/usecase/session_early_user_persist_test.go` 与
    `server/internal/api/stream_hub_turn_identity_test.go` → 用户行落盘后产生的 canonical
    `mindfs_id` 原样传给 pending；身份按 Manager 实际分配的 seq 铸造。
  - `web/src/services/session.ts` 的 `SessionBlock` 同步改名为
    `mindfs_id` / `agent_id`（`tsc --noEmit` 兜住）。

- UI 身份对账测试：web/tests/session-overlay-unit.test.mjs、web/tests/session-window.test.mjs、web/tests/message-mechanism-merges.test.mjs；服务端 wire/pending 测试：server/internal/api/stream_hub_turn_identity_test.go、stream_hub_user_message_test.go。
- **注意事项（合上游时）**：`req.TurnID`（codex SDK 的 `AskUserRequest` 字段）
  与 `ImportedExchange.NativeID` **不是**我们改名的那两个 id，别跟着一起改 ——
  前者是外部 SDK 的字段，后者是导入器的另一种表示。这两处各已踩过一次。

### G-BJ 会话列表按实时活动时间排序（2026-10-12）

- **可见症状**：回复进行中，项目组和会话行仍停在上次完成回复的时间；只有整轮结束并写回会话元数据后才跳到最前。置顶与普通会话也可能被置顶时间或项目名称排序盖过活动时间。两个会话同时在跑时，右侧项目列表每 100ms 交替跳动（activity_at 交替推进、比较器按毫秒直接比大小）。
- **根因与边界**：列表原来只看持久化 `updated_at`，而它在轮次收尾才更新。每个 stream block 到达时，前端短暂推进 `activity_at`；会话/项目的排序与时间文案都读 `max(updated_at, activity_at)`。刷新拿到旧快照时保留临时活动值，直到服务端 `updated_at` 追上再丢弃。项目组最新时间由 indexed session metadata 查询，避免用当前分页结果推断。会话置顶区和普通区各自按最新活动降序；项目排序先固定节点顺序，再在每个节点内分置顶/普通区并按活动降序，绝不跨节点比较置顶或时间。活动差距小于迟滞阈值（`ACTIVITY_REORDER_HYSTERESIS_MS = 30s`，`sessionListMerge.ts`）时视为同时在跑、保持稳定顺序不重排；差距拉开后仍按活动倒序——阈值只抑抖动、不锁死顺序。
- **打开列表不闪序**：多项目列表不再先显示 IndexedDB 旧快照再用节点请求结果修正；刷新沿用当前内存列表，首次加载由本轮节点请求填充。单项目缓存和会话内容缓存保留。
- **边界**：会话和项目列表的活动时间投影、排序及服务端项目组最新时间查询。实时正文事件归属与内容合并不变；`activity_at` 仅为前端临时字段，不写入持久化会话数据。
- **针对性测试**：
  - `web/tests/session-list-merge.test.mjs` → 活动时间覆盖旧持久时间、刷新期间不倒退、持久时间追上后清除 overlay；会话置顶/普通区内部按活动时间排序；项目先按节点分桶、再在桶内置顶优先并按活动时间排序，后续节点即使更活跃也不能越位；stream block 推进活动值且跨节点内容过滤之前执行；双活会话活动差距小于迟滞阈值时保持原序不交替、差距拉开后仍按活动倒序。
  - `web/tests/upstream-restore.test.mjs`、`web/tests/multi-account-partition.test.mjs` → 多项目列表不预显示 IndexedDB 旧快照，单项目列表账户缓存仍保留。
  - `server/internal/session/manager_latest_updated_at_test.go` → 项目组最新活动从全部可见会话（含子会话）取最大值。

### G-BK 会话模型选择与运行态一致性（2026-10-12）

- **来源**：`f7cc9af`，修复发送时模型选择与会话元数据的更新时序。
- **可见症状**：上一轮使用模型 A，用户在选择器切到模型 B 并发送后，回复期间选择器仍显示 A；整轮结束后才显示 B。已有运行中的 agent 也可能被旧的 session 快照误判为仍使用另一模型。
- **根因与边界**：
  1. 选择的模型属于会话状态，不应等回复完成才落盘。`SendMessage` 在持久化用户消息和触发 `OnStart` 前先保存请求模型，流式期间的列表/会话刷新因此读到 B。
  2. 对已有运行实例判定当前模型时，`runtime.CurrentModel()` 是活进程状态；持久化 exchange 与 session 字段依次作为运行时没有可用值时的回退。轮次完成后再用 runtime 解析出的最终模型收敛会话记录。
  3. 本组覆盖会话选择模型、活跃运行实例与持久快照之间的优先级；G-R 继续覆盖 provider 模型识别、别名探测与模型名流式透传。
- **针对性测试**：`server/internal/api/usecase/session_model_switch_test.go`：
  - `TestSendMessagePersistsSelectedModelBeforeTurnStarts` → 在 `OnStart` 时检查回调、session 与最新用户 exchange 均已是刚选择的模型 B。
  - `TestCurrentModelForRuntimeUsesLiveModelBeforePersistedSnapshot` → runtime 为 A、持久快照为 B 时，当前模型判定返回活跃 runtime 的 A。
- **涉及文件**：`server/internal/api/usecase/session.go` 的 `SendMessage` 与 `currentModelForRuntime`；`server/internal/api/usecase/usecase_test.go` 的 agent-session 测试替身；新增的模型切换回归测试。

### G-BL 看板新建任务的 worktree 节点路由（2026-10-12）

- **来源**：修正工作台快速新建任务将项目节点丢弃的问题（原入口由 `603bfc9` 接入，`0792221` 改为直接打开共享新建面板）。
- **可见症状**：从工作台为远端 Git 项目新建任务时，worktree 开关和分支选择器消失；同名项目还可能按另一节点的 Git 状态判断，并把分支请求或任务创建发到错误节点。项目看板初始加载也可能用同名项目的回退记录判断 Git 状态。
- **根因与边界**：`rootId` 在多节点间不是唯一身份。旧工作台回调虽收到 `nodeId`，却只把 `rootId` 交给新建面板；面板再通过 `byId` 的首个同名项目判断 `is_git_repo`。现在以 `nodeId::rootId` 贯穿项目选择、Git 仓库判断、分支拉取、偏好存储、附件上传、任务创建和新建 worktree 刷新。显式节点没有匹配元数据时不回退到另一节点的同名项目。
- **针对性测试**：
  - `web/tests/task-worktree-node-scope.test.mjs` → 两节点同名项目必须命中所选节点；缺失所选节点记录时不得退回另一个节点；项目作用域键可往返解析；工作台入口、worktree 判断/偏好与任务提交保留目标节点。
  - `web/tests/task-board-view.test.mjs`、`web/tests/workspace-board.test.mjs`、`web/tests/node-colors-and-workspace-preferences.test.mjs` → 共享任务面板接收并使用节点 ID，项目下拉/颜色和 worktree 分支请求都按节点区分。
- **边界**：仅看板新建任务的项目身份路由与 worktree 选择/创建链路。已有任务的执行、收尾和 worktree 生命周期策略不变。


### G-BU FileEditor 与 DiagramPreview 样式内联

- **可见症状**：两个组件依赖独立 `.css` 文件，与前端规则要求组件样式内联或使用 `*Styles.ts` 不一致；移动组件时样式依赖也容易遗漏。
- **根因与边界**：将原 CSS 文本放入组件级 `<style>`，保留原有选择器、主题变量、媒体查询、伪元素、CodeMirror 内部选择器与安全区适配。移除 CSS 文件和 import，并将测试逻辑路径映射到组件源文件。样式仍是 CSS 规则文本，避免转成 React style 对象造成选择器能力或级联行为变化。
- **针对性测试**：`web/tests/inline-component-styles.test.mjs` → 两组件样式常量与关键交互/主题选择器存在，独立 CSS 已删除，旧逻辑路径仍映射到组件。
- **边界**：仅样式载体和源码映射改变，不改 DOM 交互、组件状态或 CSS 声明。


### G-BV 实时文件变更事件职责拆分

- **可见症状**：`useRealtimeEvents.ts` 同时包含 WebSocket 订阅、会话流处理、命令结果和文件/Git/目录缓存更新；文件事件改动需要在接近千行的 hook 中查找。
- **根因与边界**：将文件变更批处理与单文件事件处理器迁入 `realtimeWsFileChangedHandlers.ts`，通过既有 `RealtimeEventsContext` 注入依赖；hook 仍负责组装和订阅。旧逻辑源映射纳入新模块。
- **针对性测试**：`web/tests/realtime-file-change-handlers.test.mjs` → handler 的缓存失效、插件/Git/目录刷新契约仍在新模块，逻辑路径映射覆盖新模块，hook 将两处理器交给 WS 注册表。
- **边界**：只移动文件变更事件实现，不改变事件字段、刷新条件、缓存或请求行为。


### G-BW 实时 slash-command 事件职责拆分

- **可见症状**：`useRealtimeEvents.ts` 在会话流处理之外还内嵌 slash-command chunk、登录通知、错误和完成状态逻辑，命令状态容易与会话状态变更混在一起。
- **根因与边界**：将 slash-command 流/完成两个 handler 搬到 `realtimeWsSlashCommandHandlers.ts`，通过现有 context 获取节点、结果 setter、session root 解析和文案函数；订阅 hook 只负责组装。
- **针对性测试**：`web/tests/realtime-slash-command-handlers.test.mjs` → chunk/login/error/done 与 request id 守卫在新模块，hook 和逻辑 source-map 均接入该模块。
- **边界**：只移动事件处理代码，不改变 request id 判定、结果状态与报告行为。


### G-BX 实时 session stream 事件处理拆分

- **可见症状**：接近千行的 `useRealtimeEvents.ts` 同时承载 hook 订阅/replay、session 回合收尾和流式块分发，消息流状态契约难以单独审查。
- **根因与边界**：把 `handleSessionStream` 主分支迁入 `realtimeWsSessionStream.ts`，通过现有 context 和回合收尾回调读写相同状态。`useRealtimeEvents` 保留 replay 目标、pending 收尾与订阅生命周期，尤其保留「队列续跑 / 是否重锚定 / 瞬时行何时退役」的原顺序。未关联的 stream 只能投影到 payload 自己的 `(root_id, session_key)`，不能以先到顺序接管 pending draft、root binding 或 drawer；请求到真实 session 的接管仅由 `session.accepted.request_id` 关联。
- **针对性测试**：`web/tests/realtime-session-stream-handler.test.mjs` → 节点过滤前的活动时间更新、节点过滤、消息块类型、compact、message_done/error 收尾契约仍在新模块；同时锁住 child/unknown stream 不得接管本地 draft、临时会话或 root binding。`session-list-merge.test.mjs`、`message-mechanism-merges.test.mjs` 和 `session-done-replay-no-reanchor.test.mjs` 继续验证相关状态行为。
- **边界**：只移动 stream handler，不改事件分发、身份键、持久化、队列、缓存与回合重锚定时序。


### G-BY 前端硬线待办按实测销项

- **可见症状**：frontend TODO 仍把已降至 392 行的 `useRealtimeEvents.ts` 列作 >500 行硬线事项，且总数保留旧计数，执行者会误以为该项尚未完成。
- **根因与边界**：按实际行数将该项从硬线列表移除，将 app 子项从 12 修正为 11、总项从 31 修正为 30，并同步 Active 计划索引。
- **针对性测试**：`web/tests/agent-docs.test.mjs` → 唯一计划索引对所有计划分类、规则文档和计划链接可解析。
- **边界**：只修正待办和计划索引的计数/状态，不改变代码或规则。


### G-BZ 将 plan 前缀逻辑移出 action view

- **可见症状**：`useSendMessage.ts` 的业务逻辑依赖 `components/action` 的 UI 域文件；plan 命令协议与 action token 编辑实现混在一个模块里，调整 UI helper 可能影响新会话发送。
- **根因与边界**：将 plan 前缀检测、解析、剥离与拼装迁入零 UI 的 `shared/planCommand.ts`，由 target-preparation app hook 与 action hook 共用；删除 `useSessionLifecycle.ts` 未使用的 `SessionViewer` import。保持原始大小写/空白规则和返回值，不改消息协议。
- **针对性测试**：`web/tests/plan-command.test.mjs` → 覆盖 `/plan` 的归一化、命令前缀边界、兼容 `null` 返回、正文剥离/拼装，并守住两个 app hook 与 shared helper 不依赖 view 模块。
- **边界**：仅迁移纯逻辑和清理无用 import；不改 UI、发送时序或服务端协议。


### G-CA 项目添加状态类型移入 shared

- **可见症状**：实时事件与 WS handler 为更新项目添加状态而从 `ProjectAddPopover.tsx` 导入类型，业务逻辑被 UI 文件路径牵连。
- **根因与边界**：把 `ProjectAddMode` 和 `GitHubImportState` 放入 `shared/projectAddTypes.ts`；实时事件逻辑直接使用共享类型，Popover 保留类型再导出供原 UI 消费者使用。
- **针对性测试**：`web/tests/project-add-types.test.mjs` → 确认 shared 类型不依赖 view/service，三个 realtime logic 模块均从 shared 导入，Popover 仍提供类型再导出。
- **边界**：只移动类型定义，不改变字段、事件 payload 或项目创建行为。


### G-CB 将新项目 meta 偏好更新移入 FileTree logic

- **可见症状**：点击文件树菜单切换新项目 meta 位置时，view 直接调用 preferences service 并控制乐观更新和错误回滚。
- **根因与边界**：将该动作移入 `useFileTreePreferences`，view 只触发 callback；保留先更新本地值、请求失败回滚、完成后清 busy 的顺序。
- **针对性测试**：`web/tests/file-tree-meta-location-action.test.mjs` → 守住 JSX 不再调用 service，logic hook 负责请求、失败恢复与 busy 收尾。
- **边界**：只移动新项目 meta 位置切换动作；其它 FileTree 逻辑与 service 引用待后续拆分。


### G-CC 将 FileTree 外观偏好交给 logic hook

- **可见症状**：外观菜单 view 直接调用 appearance service，组件选项同时负责持久化和本地状态更新。
- **根因与边界**：外观状态及写入迁入 `useFileTreeAppearance`；`AppearanceMode` 迁入 shared 并由 service 再导出，菜单选项只触发 `onChange`。
- **针对性测试**：`web/tests/file-tree-appearance-action.test.mjs` → 守住 view 不导入 appearance service、hook 执行持久化、类型共享与原 facade。
- **边界**：保持原有 storage key、DOM 主题应用和广播事件语义，只调整依赖方向。


### G-CD Agent config transition hook owns agent queries

- **可见症状**：`FileTree.tsx` 为 agent config 操作导入查询服务，再将服务函数透传给 agent config hook；UI 模块表面依赖多于实际职责。
- **根因与边界**：查询由 `useAgentConfigFlowTransitions` 自行导入和调用，FileTree 只提供状态与 setter；同时清除该组件里未使用的 agent config、WebPush 和 preferences service imports。
- **针对性测试**：`web/tests/file-tree-agent-config-flow.test.mjs` → 确认 FileTree 不再持有 agent 查询，transition hook 自己查询且不从 ctx 接收服务函数。
- **边界**：不改查询时机、force 参数、节点路由和状态转换。


### G-CE font size 纯逻辑移入 shared

- **可见症状**：字号控件依赖 preferences service 才能访问字号步进算法与范围常量，view 的依赖跨过 service 边界。
- **根因与边界**：将纯字号类型、边界和计算移入 `shared/fontSize.ts`；service 保留 localStorage 读写并重新导出旧 API，FileTree 改用 shared 入口。
- **针对性测试**：`web/tests/shared-font-size.test.mjs` 和 `web/tests/font-size-preferences.test.mjs` → 检查纯计算、不可变返回、上下限、service facade，以及持久化与渲染契约。
- **边界**：保留原步长、归一化、限幅、存储 key 与持久化行为。


### G-CF send shortcut 纯逻辑移入 shared

- **可见症状**：文件树快捷键控件依赖偏好 service 才能识别按键和展示快捷键文本。
- **根因与边界**：将快捷键类型、解析、校验、匹配和格式化迁入 `shared/sendShortcut.ts`；service 只负责存取配置并继续再导出既有 API。
- **针对性测试**：`web/tests/shared-send-shortcut.test.mjs`、`web/tests/send-shortcut.test.mjs` → 覆盖修饰键过滤、按键归一化、组合匹配、显示格式、持久化与 facade。
- **边界**：保持 modifier 规则、键名与展示顺序及持久化 key 不变。

### G-CG FileTree settings presenter 与目录排序 shared 逻辑

- **可见症状**：大型 `FileTree.tsx` 同时内嵌设置菜单 JSX；纯目录排序选项和算法经 preferences service 暴露给 view。
- **根因与边界**：将设置菜单 presenter 提取到 `FileTreeSettingsMenu.tsx`，将目录排序类型、选项与纯算法归入 `shared/directorySort.ts`；原 service 路径保留 facade。
- **针对性测试**：`web/tests/shared-directory-sort.test.mjs` → 验证排序纯度、目录优先、排序模式、旧 facade 与 FileTree presenter 的 shared 依赖。
- **边界**：菜单状态仍由调用方持有，排序偏好持久化和原有排序语义不变。

### G-CH FileTree 服务读取归入 colocated logic

- **可见症状**：FileTree 渲染时直接读取 bootstrap readiness 与 node registry；项目 tab/content presenter 接收 service 函数并执行分组、颜色回退。
- **根因与边界**：将 bootstrap 订阅、节点分组和 tab accent 解析放入 colocated logic hooks，presenter 只接收 readiness、groups 和颜色值。
- **针对性测试**：`web/tests/file-tree-service-boundaries.test.mjs` → 守住 view/presenter 不直接读取这些服务，hook 仍承担原服务读取。
- **边界**：保持同步初始 readiness、bootstrap 更新、节点排序、颜色回退顺序和空项目展示行为。

G-CH 还将默认节点强调色与调色板定义迁到 `shared/nodePalette.ts`，由 node registry service 保留原导出 facade；FileTree JSX presenter 直接使用 shared 常量。

### G-CI FileTree props 类型契约迁入 shared

- **可见症状**：FileTree 与 `fileTreeTypes` 仍从 agent/preferences service 导入类型；字号和发送快捷键的 props 类型也穿过 service facade。
- **根因与边界**：将 agent 与新项目 meta 类型放到 shared，已有 service 继续再导出；FileTree 与 props 类型统一改依赖 shared。
- **针对性测试**：`web/tests/file-tree-service-boundaries.test.mjs` → 确认 FileTree 与类型契约没有 service import，并守住 service 仍提供原 API。
- **边界**：类型结构与对外 service 导入路径不变，无运行时行为变化。

### G-CJ FileTree header menus 与 overlays presenter 拆分

- **可见症状**：settings presenter 独立后，FileTree 仍内嵌大型 header 菜单、账户面板、Agent 配置和生命周期浮层，控制文件仍超过 500 行。
- **根因与边界**：将 header menu 与相关 overlay JSX 提取到 `FileTreeHeaderOverlays.tsx`；状态与业务动作继续由 FileTree controller 持有并作为 context 传入。
- **针对性测试**：`web/tests/file-tree-header-overlays.test.mjs`、FileTree meta/service boundary tests → 守住 presenter 接线、原回调归属与 service 边界。
- **边界**：保持菜单关闭顺序、浮层可见性、回调和渲染顺序；FileTree 实测 489 行。

### G-CK FileTree 纯色转换依赖迁入 shared

- **可见症状**：FileTree 只为调用 `hexToRgbaApp` 就导入 `app/taskIcons.ts`，使 view 依赖 app 层。
- **根因与边界**：纯颜色转换放到 `shared/hexToRgba.ts`；app/taskIcons 保留原函数名并转发，FileTree、session 等通用颜色消费者直接依赖 shared。
- **针对性测试**：`web/tests/shared-hex-to-rgba.test.mjs` 与 FileTree dependency boundary test → 覆盖颜色行为、兼容 facade、FileTree 不再导入 app。
- **边界**：短/长 hex、无效值回退及 rgba 字符串透传保持原行为。

### G-CL ActionBar logic 类型与纯 helper 归位

- **可见症状**：`useActionBar` 从 TokenEditor、ModeSelector、AttachmentsArea 和混合 `styleHelpers` 导入类型/工具；多个 view 与 logic 共用纯 helper 时需依赖该 view 文件。
- **根因与边界**：ActionBar 类型及纯占位、状态、颜色、预览 helper 迁入 shared；`useResponsive` 独立归 hooks；发送、键盘、candidate 与附件 handler 抽到 `useActionBarHandlers`，controller 留状态和子 hook 编排；旧类型出口继续 re-export。
- **针对性测试**：`web/tests/action-bar-layering.test.mjs`、`web/tests/send-shortcut.test.mjs` → 覆盖 helper 文案与时序、logic 不反向导入 view、handler 接线与硬线，以及快捷键发送路径。
- **边界**：发送/上传时序不变；保持 SessionMode、TokenEditorHandle、PendingAttachment 结构和响应式断点行为。`useActionBar.ts` 当前 451 行，`useActionBarHandlers.ts` 311 行。


### G-CM TaskBoardView 契约归位与 toolbar presenter

- **可见症状**：TaskBoardView 有 645 行，并从 service/app implementation 文件导入任务、workspace、session 类型；模板 toolbar 与看板列渲染挤在同一个视图中。
- **根因与边界**：类型契约归 shared 和 task domain；appStorage/service 旧入口保留 facade；模板筛选、菜单和刷新反馈提取到 `TaskBoardToolbar`。任务呈现簇迁入 `components/task/taskIcons.tsx`，`app/taskIcons.ts` 只保留兼容 facade，非任务颜色消费者直接使用 shared helper。
- **针对性测试**：`web/tests/task-board-view-layering.test.mjs`、`task-board-view.test.mjs`、`task-card-parity.test.mjs`、`workspace-board.test.mjs` → 守住依赖方向、toolbar 接线、卡片数据契约与旧 facade。
- **边界**：保持筛选 sentinel、断点、按钮事件顺序、刷新反馈时长与任务卡展示行为；主视图现为 400 行。

### G-CN 前端 Vitest 运行链与复用件覆盖

- **可见症状**：长期规则要求 Vitest，但实际入口和部署门禁只跑 Node built-in runner；新复用件缺少直接语义测试，部署脚本也可能绕过统一测试链。
- **根因与边界**：安装与锁定 Vitest 2.1.9（兼容当前 Vite 5），`npm test` 先运行 Vitest `.test.ts`，再运行带 source-map 与 TS preload 的旧 Node `.test.mjs`；`deploy-all.sh` 统一调用 `npm test`。新复用测试覆盖模型筛选纯逻辑与 common 图标可访问语义。
- **针对性测试**：Vitest `web/tests/reusable-primitives.test.ts`；Node `web/tests/deploy-web-assets.test.mjs` 校验部署统一入口。
- **边界**：不改既有测试断言或测试行为；120 个旧 `.mjs` 测试继续由兼容 lane 执行，后续触及时逐步迁移。

### G-CO 前端 task view 硬线拆分

- **可见症状**：定时 Agent 对话框和任务卡超过前端 500 行硬线，列表状态消息、worktree 收尾按钮与父视图职责混在一起。
- **根因与边界**：定时任务列表抽为 `ScheduledAgentTaskList`，收尾按钮抽为 `TaskCardFinishButton`；对话框状态与卡片动作判据仍由原父组件持有。`ScheduledAgentTask` 类型归 shared，service 继续兼容再导出。
- **针对性测试**：`web/tests/async-state-message.test.mjs` → 确认定时任务列表继续使用 loading/empty/error 语义；`web/tests/task-card-parity.test.mjs` → 确认两个任务卡分支仍显示同一收尾动作。
- **边界**：保持原有加载/空态优先级、收尾按钮动作与渲染顺序；父组件分别为 370 与 470 行。

### G-CP ProjectAdd 模式选择面板提取

- **可见症状**：`ProjectAddPopover` 超过 500 行硬线，同时承载模式选择、目录浏览与 GitHub 导入。
- **根因与边界**：模式选择 JSX 提取到 `ProjectAddModePanel`，共享 popover 样式归同域 `projectAddStyles.ts`；LocalPanel、GitHubPanel 与状态仍由原父组件持有。
- **针对性测试**：`web/tests/project-add-types.test.mjs` → 守住三个模式标签与各自回调接线；`project-add-path-browse.test.mjs` → 守住目录浏览行为仍留在父模块。
- **边界**：保留选择回调、文案 key、布局样式与目录浏览状态；父组件现为 404 行。

### G-CQ AgentSelector trigger 展示提取

- **可见症状**：`AgentSelector` 超过 500 行硬线，菜单状态、submenu 呈现与触发按钮 JSX 同在一个文件。
- **根因与边界**：触发按钮、Agent 图标、下拉箭头、不可用徽标和紧凑悬停样式移入 `AgentSelectorTrigger`；打开/关闭与 submenu 初始化仍由父 controller 管理。旋转动画 keyframes 移到使用它的错误横幅。
- **针对性测试**：`web/tests/agent-selector-trigger.test.mjs` → 守住呈现组件 props 与父状态边界；`claude-model-1m.test.mjs` → 确认模型别名与 submenu 语义仍在原路径。
- **边界**：保持点击、鼠标按下焦点、compact hover、title、图标与 unavailable 标记；父组件现为 496 行。

### G-CR TaskInline 候选菜单呈现提取

- **可见症状**：`TaskInlineEditPanel` 超过 500 行硬线，任务创建面板与候选列表行 JSX 混在一起。
- **根因与边界**：候选列表提取到 `TaskInlineCandidateMenu`；可见性、候选索引、DOM refs 与选中回调仍由父组件传入，候选查询和任务编辑状态不移动。
- **针对性测试**：`web/tests/task-inline-candidate-menu.test.mjs` → 守住 @/#/命令显示、鼠标选择和高亮索引；task panel/node-scope tests → 守住父面板接线与 worktree 范围。
- **边界**：保留绝对定位、窄屏高度、滚动、阻止 mousedown blur 和描述文本；父面板现为 473 行。

### G-CS Login 更新说明折叠区提取

- **可见症状**：`Login` 超过 500 行，节点管理、更新按钮与更新说明折叠内容混在一起。
- **根因与边界**：更新说明的文案、后端升级提示和展开内容提取到 `LoginUpdateNotes`；展开状态、更新数据与下载动作仍由 Login/useLogin 持有。
- **针对性测试**：`web/tests/login-update-notes.test.mjs` → 守住切换文案、升级提示、内容显隐和父状态接线。
- **边界**：保持无说明时不渲染、notes 文本原样显示与现有颜色/间距；父组件现为 463 行。

### G-CT Git 状态分支选择器提取

- **可见症状**：`GitStatusPanel` 超过 500 行，分支按钮/菜单和状态项、提交/操作控件共处一文件。
- **根因与边界**：分支按钮与候选列表提取为 `GitBranchSelector`；分支查询仍由 `useGitStatusPanel` 执行，父面板持有菜单开关和当前切换状态。
- **针对性测试**：`web/tests/git-branch-selector.test.mjs` → 守住加载/空态、当前分支禁用与切换接线；`git-commit-ime.test.mjs` → 确认同一 Git 面板的提交输入仍保持原行为。
- **边界**：保留分支顺序、错误后空态、切换期间禁用、当前分支高亮和关闭时机；父组件现为 434 行。

### G-CU DocumentViewer PDF 预览职责提取

- **可见症状**：`DocumentViewer` 超过 500 行，同时承载 PDF 页面虚拟化、Office 格式解析、取文件与错误显示。
- **根因与边界**：PDF canvas/占位块生命周期移入 `PdfPreview`；统一错误组件和错误描述移入 `DocumentPreviewError`。文件取回及 Word/Excel/PowerPoint 分支仍由 `DocumentViewer` 管理。
- **针对性测试**：`web/tests/document-preview-view-split.test.mjs` → 守住 PDF 页窗口、canvas 回收和 DPR 限制；`document-preview.test.mjs` → 守住页面范围算法。
- **边界**：保持视口/resize 同步、滚动祖先查找、取消清理、错误文案和预览分派；父组件现为 318 行。

### G-CV SessionCard 溢出操作菜单提取

- **可见症状**：`SessionCard` 超过 500 行，卡片行、重命名与置顶/同步/归档/删除菜单实现交织。
- **根因与边界**：菜单触发和动作列表提取到 `SessionCardOverflowMenu`；选中态、菜单开关、重命名草稿与动作执行仍归父卡片及列表回调所有。
- **针对性测试**：`web/tests/session-card-overflow-menu.test.mjs` → 守住各动作、菜单文案和状态归属；`session-archive.test.mjs` 与 `fork-session-name.test.mjs` → 守住归档与改名链路。
- **边界**：保持菜单顺序、置顶徽标、同步禁用、归档可选项与删除回调；父组件现为 455 行。

### G-CW DefaultListView 排序菜单提取

- **可见症状**：`DefaultListViewMenu` 超过 500 行，文件操作、项目/Git 菜单和目录排序子菜单混在一起。
- **根因与边界**：排序展开按钮和 inherit/显式选项移入 `DirectorySortMenu`；`DefaultListView` 仍拥有 sort 值与 mode setter，其它菜单动作保持原组件。
- **针对性测试**：`web/tests/directory-sort-menu.test.mjs` → 守住 inherit/选项选择、关闭顺序与展开语义；命名和逻辑映射门禁覆盖新文件归属。
- **边界**：保留选项顺序、选中样式、全局继承选项和父级菜单关闭；父菜单现为 457 行。

### G-CX AgentConfig lifecycle 与样式拆分

- **可见症状**：AgentConfig 流程与生命周期管理 UI 同处大组件，FileTree overlay 也从该文件取共享按钮样式。
- **根因与边界**：`AgentLifecyclePopover` 独立并从 shared agent types 取契约；style token/helper 移到 `agentConfigStyles.ts`。运行安装/更新的业务动作仍归 FileTree hook。
- **针对性测试**：`agent-config-lifecycle-split.test.mjs` → 守住生命周期 UI、样式导出和旧 facade；`file-tree-header-overlays.test.mjs` → 守住 FileTree overlay 接线。
- **边界**：本组提交时主流程为 528 行；后续 G-CY 将它降至 450 行。旧调用路径与三项公开样式导出保持兼容。

### G-CY AgentConfig agent 选择步骤提取

- **可见症状**：生命周期 UI 移出后，`AgentConfigPopover` 仍超过 500 行，agent 清单、最近配置提示和 restart 控件仍嵌在流程组件里；view 还直接引用 service 类型。
- **根因与边界**：agent 选择 UI 独立为 `AgentConfigAgentStep`，agent/provider/backup 类型直接依赖 shared；父组件继续管理当前步骤、选中值、busy 与确认流。
- **针对性测试**：`agent-config-agent-step.test.mjs` → 守住 restart/choose 回调与列表状态；`file-tree-header-overlays.test.mjs` → 守住 FileTree facade 接线。
- **边界**：保留最近配置文本、switch flow 的 restart 行为、loading/empty 状态；父组件现为 450 行。

### G-CZ 项目生命周期 worktree overlay 呈现拆分

- **可见症状**：`useProjectLifecycle.tsx` 同时持有项目/worktree 生命周期状态与创建、切换浮层的大段 JSX，职责难以分别复核。
- **根因与边界**：创建和切换 worktree overlay JSX 移到 `WorktreeLifecycleOverlays`，生命周期 hook 仍持有状态、数据加载和所有动作；保留 App 层原有的两个独立 render 出口。
- **针对性测试**：`web/tests/worktree-lifecycle-overlays.test.mjs` → 确认 hook 接线、键盘提交/取消、加载/空态/错误顺序及当前/已管理/待加入 worktree 的标签语义。
- **边界**：不改变点击关闭、创建/切换流程、节点路由、动作时序和 overlay 放置位置；`useProjectLifecycle.tsx` 从 885 降到 731 行，尚未从 TODO 销项。

### G-DA 项目目录浏览状态和动作归入专属 hook

- **可见症状**：`useProjectLifecycle.tsx` 把本地目录浏览状态、节点路由请求、路径推导、选择和添加项目动作与 worktree 生命周期混在一起；目录状态类型也由 view 文件声明。
- **根因与边界**：目录浏览职责抽到 `useProjectDirectoryBrowser`，`LocalDirItem`/`LocalDirBrowserState` 移到 shared；ProjectAddPopover 保留旧类型再导出，生命周期 hook 继续组合项目模式和 worktree 流程。
- **针对性测试**：`project-add-path-browse.test.mjs` → 守住 nodeId 路由、目录添加/刷新/打开和已管理目录选择；`project-add-types.test.mjs` → 守住 shared 契约及旧导出。
- **边界**：保留目录 API 参数、Windows/Unix 路径推导、空态/error/选择重置和刷新顺序；该批提交时父 hook 实测 571 行，仍高于 500 行硬线。

### G-DB 项目添加 action 与 overlay 组合归位

- **可见症状**：目录浏览已分离后，`useProjectLifecycle.tsx` 仍将项目添加模式回调、GitHub 导入请求与 popover 的 props/布局组合留在 worktree 生命周期 hook 中。
- **根因与边界**：项目添加动作集中到 `useProjectAddActions`，原有 `ProjectAddPopover` 增加组合 overlay 入口；生命周期 hook 只向其传状态和回调，继续协调 root/worktree 创建。
- **针对性测试**：`project-add-types.test.mjs` → 确认 GitHub 节点路由/提交状态及 view 回调接线；项目添加路径和类型测试继续守住目录浏览契约。
- **边界**：保留 GitHub 请求条件、状态清理、节点路由、toast 文案、弹层 ref 与模式转换；`useProjectLifecycle.tsx` 实测 482 行，低于 500 行硬线。

### G-DC 会话域聚合 hook 按职责拆分

- **可见症状**：`useAppSessionLayer.ts` 同时声明 139 行公共参数类型，并串接 session lifecycle/pending/metadata、multi-project、stream cache、reply poll 等逻辑，超过硬线。
- **根因与边界**：参数契约移至 `useAppSessionLayerTypes.ts`；session key/pending/lifecycle/metadata/multi-project/stream-cache 组合移至 `useAppSessionCore`；multi-project debounce/reply poll 移到 `useAppMultiProjectReload`，调用位置保持在原顺序上。父层保留功能组合，降至 474 行。
- **针对性测试**：`web/tests/app-session-layer-split.test.mjs` → 守住父层硬线、核心 hook 责任组、multi-project reload 接线顺序与 App 层调用。
- **边界**：只重组 hook 文件与契约，不改变子 hook 参数、状态/ref、debounce 时长或 App.tsx 组合根。

### G-DD Domain layer 参数契约和项目管理组拆分

- **可见症状**：`useAppDomainLayer.ts` 声明大段层间参数并组合 root 管理、action handler、bound-session 恢复、文件导航、项目删除及 project/worktree 生命周期，超过硬线。
- **根因与边界**：参数契约进入 `useAppDomainLayerTypes.ts`，原模块保留 type re-export；原连续 hook 序列抽到 `useAppProjectManagement`，parent 继续组合插件、任务、bootstrap、realtime 与 derived 域。
- **针对性测试**：`app-domain-layer-split.test.mjs` → 守住 500 行硬线、类型兼容出口、项目管理 hook 归属与 App 调用入口。
- **边界**：hook 序列在 parent 原调用位置整体移动，不改变 action ref、managed-root 更新、节点失败通知、导航或 worktree 生命周期行为；父层现为 437 行。

### G-DE App 状态层命令式 ref 集合拆分

- **可见症状**：`useAppStateLayer` 同时声明会话、目录、插件、drawer、缓存等大量命令式 ref，混在状态层 hook 组装中，难以单独核对状态所有权和初始化顺序。
- **根因与边界**：命令式 ref 与已有 session cache hook 初始化移至 `useAppStateRefs`；父层只在原位置接收 ref 集合，不改变 refs 名称、对象或后续写入点。
- **针对性测试**：`web/tests/app-state-refs-split.test.mjs` → 守住父层接线、缓存与 refs 初始化顺序及文件滚动位置恢复。
- **边界**：这是 `useAppStateLayer` 的分阶段拆分；父层当前仍 762 行，仍列在硬线 TODO，后续继续按状态域拆分。

### G-DF 项目 root 选择与跨节点清理职责拆分

- **可见症状**：状态聚合 hook 内嵌项目节点选择动作，同时处理请求取消、会话/看板清理、活动节点路由和 managed-root 映射，父层仍高于硬线。
- **根因与边界**：选择回调迁入 `useAppRootSelection`；父层在相同 hook 序列位置接收 callback。原先后置声明的 session timer 与 Git setter 通过 getter 延迟读取，避免移动声明或改变 hook 顺序。
- **针对性测试**：`web/tests/app-root-selection-split.test.mjs` → 守住回调归属、请求取消、跨节点缓存清理和 managed-root 映射；`task-template-node-routing.test.mjs` → 守住 active-node 与所选项目一致。
- **边界**：不改 `App.tsx`、请求作用域、清理条件或数据写入；父层现为 714 行，继续留在硬线 TODO。

### G-DG 文件与工作区状态组抽离

- **可见症状**：`useAppStateLayer` 的中段连续混合目录编辑/展开、Git 状态与历史、文件查看器、插件状态、项目 worktree 树和更新状态，父层超过硬线。
- **根因与边界**：该连续状态组与 Git/project-tree hooks 一并迁入 `useAppFileWorkspaceState`，父层以同位置的一次调用接收原变量，Ref 镜像、localStorage 恢复、插件查询初始化和 worktree 刷新转发顺序保持。
- **针对性测试**：`web/tests/app-file-workspace-state.test.mjs` → 守住职责接线、Git/tree hooks、目录 ref 镜像及 worktree 刷新转发；`node-colors-and-workspace-preferences.test.mjs` → 守住字面量类型与偏好契约；`source-map.test.mjs` → 守住逻辑模块映射。
- **边界**：不改 `App.tsx`、服务调用或渲染数据；父层现为 588 行，仍留在硬线 TODO。

### G-DH 拆分源码守卫的物理行数读取

- **可见症状**：拆分后的 source-map 逻辑别名合并父层与子模块源码，直接用它统计父层长度会把子模块行数计入，导致硬线结构断言失真。
- **根因与边界**：`app-state-refs-split` 与 `app-root-selection-split` 改用文件描述符读取物理父文件测行数；其它源码断言继续走逻辑映射，以覆盖合并后的职责行为。
- **针对性测试**：`app-state-refs-split.test.mjs`、`app-root-selection-split.test.mjs` → 用物理父文件统计行数，同时保留逻辑模块接线与行为守卫。
- **边界**：只调整测试读取方式，不改应用代码；保持 `useAppStateLayer` 的 588 行真实计数。

### G-DJ App 状态层 session 与 root setup 按域拆分

- **可见症状**：`useAppStateLayer` 仍内联会话列表/归档、选中会话、响应式壳层、root scope 和任务模板候选初始化，虽此前多个跨域组已抽出，父层仍超过 500 行。
- **根因与边界**：上述职责分别归 `useAppSessionListState`、`useAppSessionSelectionState`、`useAppResponsiveState`、`useAppRootScopeState`、`useAppTaskTemplateState`；父层保留各 hook 的原调用顺序与返回字段。
- **针对性测试**：`web/tests/app-state-domains-split.test.mjs` → 守住各职责归属、hook 顺序、session 常量字面量、pin 初始化及任务候选接线；source-map 与命名测试验证拆分映射和文件约定。
- **边界**：不改 `App.tsx` 或 session/task/root 行为；`useAppStateLayer` 实测 498 行，硬线 TODO 销项。

### G-DK Session lifecycle 按动作职责拆分

- **可见症状**：`useSessionLifecycle` 同时处理会话恢复、列表分页/缓存、重命名和手动同步，超过前端 500 行硬线；手动同步若漏接缓存中的瞬时尾部，会让 ask 卡或正在输出的正文在点击同步后消失。
- **根因与边界**：列表头同步、根会话列表与子会话分页归 `useSessionListLoading`；全量手动同步及瞬时尾部去重归 `useSessionSyncAction`。两个 hook 在旧 callback 组合位置调用，`useSessionLifecycle` 只保留恢复、选择和重命名编排。
- **针对性测试**：`web/tests/session-lifecycle-split.test.mjs` → 守住父 hook 物理行数、组合顺序、子会话 node 标记与瞬时尾部归属；`session-list-merge.test.mjs`、`session-load-storm.test.mjs`、`sync-preserves-pending-ask-card.test.mjs`、`sync-preserves-live-transient-text.test.mjs` → 守住节点归并、并发列表读取、ask 卡与流式正文续接。
- **边界**：不改 `App.tsx`、请求路由、列表合并、窗口恢复或同步时的去重规则；父 hook 实测 740→404 行。

### G-DL 托管根 ID 重映射职责拆分

- **可见症状**：托管根 ID 改名后，若项目级缓存键只迁移一部分，会话、目录树或展开状态会留在旧 ID 下；`useManagedRoots` 同时装载根、清理状态、重映射缓存和处理显示名，超过 500 行硬线。
- **根因与边界**：物理根 ID 变化时跨 session/tree/cache 的 key remap 归 `useManagedRootRename`；`handleRenameCurrentRoot` 仍负责请求与同 ID 显示名更新，并仅在服务端返回新 ID 时调用 remap。参数契约移到 `useManagedRootsTypes`。
- **针对性测试**：`web/tests/managed-roots-split.test.mjs` → 守住父 hook 行数、回调接线与 session/tree cache remap；`app-domain-layer-split.test.mjs` 与 source-map 门禁确认 App project-management 组合入口保持可解析。
- **边界**：不改变服务端改名请求、同 ID 的 display-name 原地更新、缓存 key 格式或 `App.tsx`；父 hook 实测 709→480 行。

### G-DM 发送消息按准备与瞬时命令拆分

- **可见症状**：发送消息 handler 同时选择/初始化会话、处理 `/status`/`/login`、写入乐观消息、组装插件上下文并提交传输；若流程时序漂移，新会话可能未选中、pending 不收敛或瞬时命令一直显示运行中。
- **根因与边界**：目标会话解析、队列/pending 判定和新会话草稿准备归 `prepareSendMessageTarget`；瞬时 slash command 归 `useSendMessageSlashCommand`；普通消息乐观写入、上下文构建、发送和失败回滚仍归 `useSendMessage`。参数契约移到 `useSendMessageTypes`，原模块保留再导出。
- **针对性测试**：`web/tests/send-message-split.test.mjs` → 守住父 hook 硬线、阶段顺序、契约兼容与职责归属；`new-session-auto-select.test.mjs`、`pending-single-source.test.mjs`、`plan-command.test.mjs` → 守住新会话选择、pending 唯一真相和计划模式前缀。
- **边界**：请求参数、pending 写入/失败收敛、插件 view context 与 `App.tsx` 均不变；父 hook 实测 686→422 行。

### G-DN App 视图组合按工作台职责拆分

- **可见症状**：`useAppViewComposition` 同时组合会话/根内容视图与跨项目工作台状态、实时任务新鲜度、筛选/折叠持久化和刷新转发，超过前端硬线，工作台跨节点更新路径难以单独核对。
- **根因与边界**：参数契约移入 `useAppViewCompositionTypes`，旧模块保留类型再导出；`useAppWorkspaceBoardComposition` 接管既有工作台状态、state 派生 live task、board hook 与 refresh ref 写入。它在原 hook 序列位置、`useAppMainViewActions` 之前调用，`App.tsx` 与行为保持不变。
- **针对性测试**：`app-view-composition-split.test.mjs` → 守住物理父 hook 行数、兼容类型出口、调用顺序、state 派生新鲜度和 refresh ref；`workspace-realtime-freshness.test.mjs`、`workspace-board.test.mjs` → 守住实时增量和看板契约；`source-map.test.mjs` → 守住逻辑源码映射。
- **边界**：父 hook 实测 665→433 行，frontend 硬线 TODO 剩余 8 项；不改 `App.tsx`、节点路由、刷新频率或看板持久化语义。

### G-DO 会话流缓存按 runtime metadata 职责拆分

- **可见症状**：`useSessionStreamCache` 同时负责 cache upsert、runtime 元信息解析、agent 流身份合并、各类 exchange merge 与 context-window 投影，645 行超过前端硬线，最敏感的事件身份路径难单独核对。
- **根因与边界**：`useSessionStreamRuntimeMeta` 接管原有防抖 bump ref、通用 upsert、runtime metadata 候选解析和 agent chunk 合并；`SessionRuntimeMeta` 移入 type contract，旧 hook 原样再导出。Thought/tool/todo/plan/compact merge 与 context-window 更新留在父 hook，新 hook 在原调用位置执行。
- **针对性测试**：`session-stream-cache-split.test.mjs` → 守住父 hook 行数、旧类型出口、职责归属与 mindfs_id 合并；`message-mechanism-merges.test.mjs`、`claude-model-1m.test.mjs`、`session-duration.test.mjs`、`upstream-restore.test.mjs` → 守住流身份、模型元信息、事件时间和 userShell replay；`source-map.test.mjs` → 守住逻辑源码映射。
- **边界**：父 hook 实测 645→473 行，frontend 硬线 TODO 剩余 7 项；`App.tsx`、事件顺序与其它 role 合并语义不变。

### G-DP Action handlers 按目录加载职责拆分

- **可见症状**：`useActionHandlers` 把打开文件与目录/树导航组合在同一个大 memo 回调中，超过前端硬线；目录切根、树缓存、绑定会话恢复及错误状态难单独审阅。
- **根因与边界**：原 `open_dir` callback body 搬到 `createOpenDirectoryAction`，接受既有完整参数面及 `isMobile`/翻译函数；父 hook 仍在原 `useMemo` 内创建它。打开文件 callback、参数契约、memo 依赖、根/节点路由、树缓存、绑定会话恢复和失败处理保持。
- **针对性测试**：`action-handlers-split.test.mjs` → 守住物理父 hook 行数、参数兼容、memo 创建点、ref 自递归与失败状态；`main-content-view-memory.test.mjs` → 守住视图内存行为；`source-map.test.mjs` → 守住逻辑源码映射。
- **边界**：父 hook 实测 611→409 行，frontend 硬线 TODO 剩余 6 项；不改 `App.tsx`、打开文件行为或目录导航语义。

### G-DQ Realtime WS 按任务事件域拆分

- **可见症状**：`realtimeWsHandlers` 同时处理 session 流与任务更新、worktree 清场、删除事件。任务组还包含跨节点过滤、详情增量、工作台刷新和收尾结果反馈，事件注册表难单独审阅。
- **根因与边界**：`task.updated`、`task.finish_teardown`、`task.deleted` 原处理体归 `createRealtimeWsTaskHandlers(ctx)`，在原事件表位置（`session.user_message` 后、`file.changed.batch` 前）展开；父工厂只移除任务专属 context 解构字段。
- **针对性测试**：`realtime-ws-task-handlers-split.test.mjs` → 守住父工厂物理行数、事件注册位置、节点隔离、工作台刷新和详情清理；`task-card-parity.test.mjs`、`worktree-finish.test.mjs`、`workspace-realtime-freshness.test.mjs` → 守住任务/收尾/实时行为；`source-map.test.mjs` → 守住逻辑源码映射。
- **边界**：父 hook 实测 570→426 行，frontend 硬线 TODO 剩余 5 项；不改 `App.tsx`、WS 派发线程或业务行为。

### G-DR SessionPane 按主内容组合职责拆分

- **可见症状**：`SessionPane` 同时持有固定双面板壳、聊天/插件信任空态、插件渲染、文件预览、Git diff 与目录/看板组合，超过前端硬线并混合壳层显隐与视图分流。
- **根因与边界**：`SessionMainContent` 接管原主内容分支和优先级；插件信任 JSX 归 `PendingPluginTrustView`；props contract 归 `sessionPaneTypes` 并由旧路径再导出。外层保留原双 pane display predicate，并在同一内容槽渲染组合组件。
- **针对性测试**：`session-pane-split.test.mjs` → 守住两个组合文件低于硬线、类型兼容出口、显隐和信任按钮接线；`main-content-view-memory.test.mjs`、`node-colors-and-workspace-preferences.test.mjs` → 守住视图状态与节点色契约；`source-map.test.mjs` → 守住逻辑源码映射。
- **边界**：原 `SessionPane` 682→58 行，内容组合 458 行，frontend 硬线 TODO 剩余 4 项；不改 `App.tsx`、pane 显隐条件或视图优先级。

### G-DS SessionViewer 按滚动状态与时间线元数据拆分

- **可见症状**：`SessionViewer` 同时管理滚动锚定、自动跟随、visual viewport 补偿、stickiness 与用户时间线元数据，超过前端硬线；改滚动逻辑可能破坏翻页前插、深链定位或「回到最新」按钮。
- **根因与边界**：`useSessionViewerScrollState` 接管原 scroll refs/state/callback；scroll position/viewport 与 stickiness effects 在旧调用位置分别交给对应 hook；`useTargetSeqScroll` 保持原 refs 与调用点。时间线元数据的一次 O(n) 扫描移到纯 helper，动画 keyframes 保持同声明并归入现有 styles。
- **针对性测试**：`session-viewer-scroll-split.test.mjs` → 守住物理行数、旧 hook 调用点、近底判断/用户索引与动画样式；`session-window.test.mjs`、`session-window-overlay-dedup.test.mjs`、`session-switch-flicker.test.mjs` → 守住窗口分页、overlay 去重与会话切换滚动；`source-map.test.mjs` → 守住逻辑源码映射。
- **边界**：父组件实测 646→499 行，frontend 硬线 TODO 剩余 3 项；`SessionViewer` memo comparator、窗口数据与 `App.tsx` 均不变。

### G-DT Markdown viewer 受保护图片职责拆分

- **可见症状**：`useMarkdownViewer` 同时负责 markdown parse/render/outline/navigation 与受保护图片路径解析、blob 请求和 object URL 生命周期，超过前端硬线，图片资源清理难单独检查。
- **根因与边界**：图片 source effect、受保护 blob 获取、object URL 创建/撤销及 fallback/图片呈现迁入 `MarkdownImage`；父 hook 原样传入 src/path/root/alt/title/width，并保留解析管线、链接导航、outline 和 hook API。
- **针对性测试**：`markdown-viewer-split.test.mjs` → 守住父 hook 行数、接线与图片 URL 生命周期；`source-map.test.mjs`、`naming-convention.test.mjs` → 守住逻辑源码映射和组件命名规则。
- **边界**：父 hook 实测 527→445 行，frontend 硬线 TODO 剩余 2 项；不改 markdown sanitizer、外链策略或 `MarkdownViewer` 消费者。

### G-DU Session connection 出站命令职责拆分

- **可见症状**：`SessionConnectionManager` 把 WebSocket 生命周期/重连状态与 session、queue、plan mode、slash-command、answer-question 出站命令混在一起，603 行超过前端硬线，改 payload 时难把命令行为与连接健康状态分开核对。
- **根因与边界**：`SessionConnectionOutbound` 接收 socket-open 判据、传输回调、共享 pending map/context cache，负责原出站命令方法；manager 保留公开 API 的 typed delegator，并继续拥有 socket 状态、ack 删除、reconnect 计时和 pending resend。sendMessage 的 open 守卫仍先于 context 压缩/pending 写入。
- **针对性测试**：`session-connection-outbound-split.test.mjs` → 守住父类行数、API 代理、pending ack/resend 归属及发送守卫顺序；`session-window.test.mjs` → 守住 probe/reconnect 状态；`source-map.test.mjs`、`frontend-split.test.mjs` → 守住源码映射与服务拆分目录契约。
- **边界**：父类实测 603→472 行，frontend 硬线 TODO 剩余 1 项；不改公开方法参数、消息 payload 或重连行为。

### G-DV Session stream 时间线投影职责拆分

- **可见症状**：`useSessionStream` 把 React 订阅/定时器状态与时间线投影、exchange/block 拼接、工具分组及 context-window 回退混在一起，648 行超过前端硬线，纯投影规则难以独立验证。
- **根因与边界**：时间线投影归 `sessionTimeline.ts`，assistant exchange segment 投影归 `sessionAssistantTimeline.ts`，稳定 ID 归 `stableTimelineID.ts`；原 hook 保留订阅、节流 streamVersion 和公开 hook API，并从旧路径继续导出 `TimelineItem` 与 `groupConsecutiveToolCalls`。三个实现模块分别为 332/184/18 行，消费者导入路径不变，均符合 500 行硬线。
- **针对性测试**：`session-stream-timeline-split.test.mjs` → 守住拆分边界、行数与兼容导出；`tool-card-grouping.test.mjs` → 守住工具折叠语义；`session-timeline-seq.test.mjs`、`message-mechanism-merges.test.mjs` → 守住持久 seq 与 context-window 优先级。
- **边界**：原 hook 实测 648→128 行，时间线/assistant/ID helper 分别 332/184/18 行；stream 订阅、pending 闸门和事件顺序不变。


### G-DW Session usecase 列表与搜索职责拆分

- **可见症状**：`usecase/session.go` 将列表、跨根聚合、子会话展开、shell 补全、搜索排序与 session 创建、fork、实时流和取消流程混在同一 4,892 行文件中。
- **根因与边界**：列表/搜索入口及私有 helper 原样移入 `session_list.go`（300 行）；公共输入/输出类型留在原文件。`session.go` 当前 4,602 行，仍高于 1,500 行硬线，后续按领域继续拆分，不在本批销掉 TODO。
- **针对性测试**：`usecase_test.go` 的 multi-root 搜索全局限额/RootID、列表 AfterTime 和 archived-only 用例；整个 `go test ./server/internal/api/usecase/...` 通过。

### G-DX Session usecase fork 与历史复制职责拆分

- **可见症状**：`ForkSession` 与历史/块复制 helper 占据 `session.go` 400 余行，fork 分支和历史重建行为与 agent turn 编排混杂，修改 fork 时难以审阅。
- **根因与边界**：`CreateSession`、`ForkSession`、fork target/model/name 解析和 legacy/block history copy 原样迁入 `session_fork.go`（426 行）；公共输入输出类型留在 `session.go`。主文件实测 4,189 行，backend TODO 继续保持未完成。
- **针对性测试**：`server/internal/api/usecase/usecase_test.go` → 覆盖 fork/session usecase 契约；`go test ./server/internal/api/usecase/...` 通过。

### G-DY Session usecase read/query 职责拆分

- **可见症状**：会话读取 API、窗口 aux/block 查询、tool/context-window 查找、related-file 规范化与旧 worktree 路径处理都混在超大 `session.go`，读路径修复需同时穿过写入和实时 agent turn 编排。
- **根因与边界**：这些读/查询方法及路径 helper 原样迁至 `session_read.go`（265 行）；公开输入/输出类型和 Service API 不变。父文件实测 3,936 行，backend TODO 继续未完成。
- **针对性测试**：`usecase_test.go` 与 `http_window_test.go` → 守住会话窗口与读取契约；`go test ./server/internal/api/usecase/...` 通过。

### G-DZ Session usecase 生命周期与级联清理职责拆分

- **可见症状**：close/delete/archive/rename 及递归子树清理与 prompt 构建、实时 agent turn 编排混在超大 `session.go`；删除和归档的资源清理顺序难以整体审阅。
- **根因与边界**：close、delete、archive、rename、子树枚举/清理和 active-turn cancel 原样迁至 `session_lifecycle.go`（252 行）；delete 与 archive 仍共用级联 helper，顺序和 API 契约不变。父文件实测 3,696 行，TODO 继续未完成。
- **针对性测试**：`server/internal/api/usecase/usecase_test.go` → 守住 close/delete/archive/rename 契约；`web/tests/session-archive.test.mjs` → 守住 ArchiveSession 子树删除和 handler 解绑接线；`make test` → 全仓 Go 测试门禁通过。

### G-EA Session usecase prompt 构建职责拆分

- **可见症状**：`BuildPrompt`、switch-read 前缀、user/plugin prompt 拼装与插件输出约定和 send 状态、agent 事件处理混在 `session.go`，文本格式和路径选择难以单独审阅。
- **根因与边界**：prompt API 与纯组装 helper 原样迁至 `session_prompt.go`（265 行）；`BuildPromptInput` 与调用契约不变，prompt 顺序、插件目录选择、reply tips、switch-read 行数规则不变。父文件实测 3,441 行，backend TODO 继续未完成。
- **针对性测试**：`server/internal/api/usecase/usecase_test.go` → 守住 BuildPrompt 的 reply tips、plugin prompt 与首轮/switch-read 契约；完整 `make test` → 全仓 Go 测试门禁通过。

### G-EB Session 命名生成与候选整理职责拆分

- **可见症状**：标题生成的 agent 调用、偏好覆盖、会话元数据更新、候选清理/评分与 fallback 标题构造混在 `session.go` 的 send/turn 流程旁，命名规则难单独审阅。
- **根因与边界**：`sessionNameRunner`、`SuggestSessionName`、fallback/prompt 生成和候选评分 helper 原样迁入 `session_name.go`（197 行）；输入契约、agent 偏好优先级和生成行为不变。父文件实测 3,256 行，backend TODO 继续未完成。
- **针对性测试**：`usecase_test.go` → 守住命名评分/候选归一、agent runner 与无 agent 跳过行为；完整 `make test` → 全仓 Go 测试门禁通过。

### G-EC Session runtime/model 解析职责拆分

- **可见症状**：agent session 重开判定、运行时 model reconcile、显示名解析、Claude 环境投影以及 mode/effort/fast-service 推断混在 `session.go` 的 send/stream 编排旁，运行时默认优先级难以集中审阅。
- **根因与边界**：runtime reopen/model/display-name/environment 与 mode/effort/fast-service helper 原样迁入 `session_runtime.go`（215 行）；各 provider 的缺省值和覆盖顺序不变。父文件实测 3,052 行，backend TODO 继续未完成。
- **针对性测试**：`server/internal/api/usecase/usecase_test.go` → 守住 model、mode、effort、fast-service 与 runtime reopen 决策；完整 `make test` → 全仓 Go 测试门禁通过。

### G-ED Session turn identity 与持久化职责拆分

- **可见症状**：exchange identity 对账、importer/live 判重、用户 turn 提前落盘、MindFS turn ID 生成和 block 写入与 `SendMessage` 混在 `session.go`，身份回归会静默造成 turn 重复或缺失。
- **根因与边界**：身份/正文比较、user turn 持久化、turn ID 和 block persistence helper 原样迁至 `session_turn_identity.go`（359 行）；`SendMessage` 调用顺序不变。父文件实测 2,706 行，backend TODO 继续未完成。
- **针对性测试**：`session_identity_dedup_test.go`、`session_early_user_persist_test.go`、`session_blocks_write_test.go`、`exchange_repeat_test.go` → 守住 turn 身份、重发判重和块写入；完整 `make test` → 全仓 Go 测试门禁通过。

### G-EE Session SendMessage 主流程职责拆分

- **可见症状**：`SendMessage` 同时负责输入校验、turn 锁、user 持久化、runtime 初始化、agent 启动、流事件、工具/关联文件更新、结束清理，堆在超大 `session.go` 单个函数中。
- **根因与边界**：`SendMessage` 方法体原样迁至 `session_send.go`（444 行），继续调用现有 turn identity/runtime helpers；方法签名、持久化/回调顺序、错误和取消语义不变。父文件实测 2,276 行，backend TODO 继续未完成。
- **针对性测试**：`usecase_test.go`、`session_early_user_persist_test.go`、`session_identity_dedup_test.go` 和 `user-message-persist-timing.test.mjs` → 守住发送、user 先落盘和身份判重；完整 `make test` 通过。

### G-EF Claude subagent 路由职责拆分

- **可见症状**：Claude synthetic subagent 创建、ref/key 归一、终态摘要、子会话事件路由和 synthetic Agent Session 实现与主 session turn pipeline 混在 `session.go`，父子会话行为难集中审阅。
- **根因与边界**：router 生命周期/事件 helper 与 synthetic session 方法原样迁入 `session_subagents.go`（315 行）；事件注册和调用顺序不变。父文件实测 1,973 行，backend TODO 仍未完成。
- **针对性测试**：`server/internal/api/usecase/usecase_test.go` → 守住 subagent 创建、任务通知摘要和 chunk 路由；完整 `make test` → 全仓 Go 测试门禁通过。

### G-EG Session answer 与 stream aux helper 职责拆分

- **可见症状**：`AnswerQuestion` 与工具 aux hydration、去重/状态合并及回复 chunk helper 挤在 `session.go`，问题回答路径和 stream 写盘语义难以分开检查。
- **根因与边界**：`AnswerQuestion`、pending tool aux hydrate/dedupe/merge 与 response chunk helper 原样迁入 `session_stream_events.go`（202 行）；调用顺序和 tool 状态持久化策略不变。父文件实测 1,782 行，backend TODO 继续未完成。
- **针对性测试**：`server/internal/api/usecase/usecase_test.go` → 守住工具 aux 合并/去重、回答和行号投影；完整 `make test` → 全仓 Go 测试门禁通过。

### G-EH Session event 与 model validation 拆分并清除父文件硬线

- **可见症状**：agent event 元数据提取、subagent 标题、affected path/diff path 转换、related-file 投影与 Claude model 支持验证混在 `session.go`；worktree→项目根映射和 provider model 规则难与主 turn 编排分开审阅，父文件超过 1,500 行硬线。
- **根因与边界**：subagent metadata/title、event compaction、tool/diff/related-file path normalization 原样迁入 `session_agent_events.go`（209 行）；Claude alias 归一和 `validateAgentModel` 原样迁入 `session_model_validation.go`（136 行）。父文件实测 1,455 行；事件顺序、路径映射、模型错误语义和 cancel 生命周期不变。
- **针对性测试**：`server/internal/api/usecase/usecase_test.go` → 守住 related-file/worktree 路径投影、Claude alias、`[1m]` 后缀及 provider 支持模型校验；相关 usecase 测试与全量 `make test` 已通过。

### G-EI Session manager 搜索职责拆分

- **可见症状**：`manager.go` 同时放着搜索入口、名称匹配排序、块流/旧 JSONL 正文扫描、命中打分和摘要截取；搜索语义与会话持久化及生命周期实现混在 3,593 行文件中。
- **根因与边界**：`Search` 与全部 search-specific helpers 原样迁至 `manager_search.go`（322 行）。公开 API、同包私有测试调用、块流优先后回退旧日志的顺序、评分和错误行为不变；`manager.go` 仍有 3,593 行，继续按领域拆分。
- **针对性测试**：`manager_search_test.go`、`search_blocks_test.go` → 守住超长 legacy JSONL 行、块流正文/推理命中、user 优先、seq 定位、工具元数据不参与搜索及 legacy fallback；session 包专项测试已通过。

### G-EJ Session manager 主 exchange 游标与增量读取拆分

- **可见症状**：主 exchange JSONL 的全量/尾部读取、重复 seq 合并、缓存游标推进和 append 写入与 session meta/binding 混在 `manager.go`，改热路径容易漏掉全量与增量两条路径共用的判重规则。
- **根因与边界**：`exchangeFileCursor`、主日志加载、NUL 前缀修复、seq 合并、全量/增量读取、序列 helper、append 和游标推进原样迁入 `manager_exchanges.go`（315 行）。cache freshness 与 aux 读仍留在父文件，供后续单独拆分；主文件实测 3,289 行。
- **针对性测试**：`manager_cursor_test.go`、`manager_incremental_merge_test.go`、`manager_duplicate_seq_test.go`、`manager_window_test.go`、`manager_window_cold_test.go`、`manager_test.go` → 守住游标恢复、尾部字段升级/重复 seq 判重、窗口冷读不触发全量解析、NUL 行修复和主日志读写行为。

### G-EK Session manager aux 增量读与窗口尾读拆分

- **可见症状**：aux JSONL 的全量读取、游标增量读取、窗口尾部反向扫描、seq 过滤和窗口轻压缩与 exchange、SQLite 实现混在父文件中；误改回退路径会让窗口读取变成整份 aux 解析或丢失懒加载载荷约定。
- **根因与边界**：`loadExchangeAux`、窗口过滤、cursor-aware incremental read、全量/尾部读取及窗口尾扫描原样迁至 `manager_aux_reads.go`（274 行）。公开 wrapper 与 pending tool-call state 留在父文件；全量 fallback、轻压缩和 seq 过滤顺序不变。父文件实测 3,026 行。
- **针对性测试**：`aux_window_tail_test.go`、`manager_window_test.go`、`manager_aux_tail_test.go` → 守住尾部窗口与全量结果等价、空文件/缺失文件、懒展开 payload 与不触发全量 aux 解析。

### G-EL Session manager 命令 shell 有界 aux 尾读拆分

- **可见症状**：命令会话列表标签需要取最新 `userShell` 状态；若读整个 aux 文件，列表每个命令会话都可能持锁扫描数 MB 数据。
- **根因与边界**：`GetCommandShell`、aux seq 分组和行边界安全的 `readAuxTailEntries` 原样迁至 `manager_aux_tail.go`（120 行）。仍以尾部命中判断 shell，仅在尾读不可用时回退全量加载；父文件实测 2,916 行。
- **针对性测试**：`manager_aux_tail_test.go` → 守住 bounded tail 命中与不触发全量 aux parse；`usecase_test.go` → 守住列表页 shell 标签推断接线。

### G-EM Session manager pending tool-call aux 状态拆分

- **可见症状**：流式中的 tool-call 详情要先从内存 pending 状态返回，写盘后再从 aux 重建；ask-user 答案需合并到同一个调用。若锁或内存优先顺序漂移，展开中的工具卡会短暂丢详情，或已填写答案被覆盖。
- **根因与边界**：`GetFullToolCall`、pending aux upsert/answer/clear、tool-call merge、pending lookup 与 clone helper 原样迁至 `manager_pending_aux.go`（192 行）。内存先查、磁盘回退、answers 累并和锁边界保持；aux append/path 与公开读 wrapper 留给后续批次。父文件实测 2,735 行。
- **针对性测试**：`manager_test.go` → 守住 pending 优先于磁盘、clear 后 not-found、ask-user 多次回答累并及 tool-call aux 落盘读取。

### G-EN Session manager aux JSONL 写入与路径 helper 归位

- **可见症状**：aux 文件的 JSONL append 与 session key/path 校验留在 `manager.go`，读逻辑已在 aux 专属模块，后续存储修改需要跨文件寻找同一职责。
- **根因与边界**：`appendExchangeAux` 与 `auxPath` 原样并入 `manager_aux_reads.go`；`AddExchangeAux` 的公开校验和 mutex 边界未改，exchange 主日志写入仍归 `manager_exchanges.go`。父文件实测 2,702 行。
- **针对性测试**：`manager_test.go` → 守住 aux 正文/工具/plan/todo 存储、key 路径验证与读回行为。

### G-EO Session manager agent binding 持久化与查询拆分

- **可见症状**：列表推断使用中的 agent、外部 transcript cursor 续读和 binding 反查都依赖 `session_agent_bindings`，但其读写散落在 `manager.go`，改 schema 或写入时序难以整体核验。
- **根因与边界**：agent state/binding upsert、external cursor update、lookup/ensure/list 与 row scan/upsert helper 原样迁至 `manager_agent_bindings.go`（258 行）。数据类型、SQL schema 与写入语义不变；repoint 事务继续留在既有方法。父文件实测 2,456 行。
- **针对性测试**：`manager_test.go` → 守住创建时绑定、agent 推断、空 transcript id 和幂等 ensure；`manager_cursor_test.go`、`manager_cursor_migration_test.go` → 守住外部游标字段与旧 schema 迁移。

### G-EP Session manager SQLite 初始化与迁移职责拆分

- **可见症状**：session DB 的惰性打开、schema 迁移顺序、旧 alias/fork 元数据归一和 DB link 文件路径共存于大 `manager.go`，持久化兼容性修复难集中审阅。
- **根因与边界**：SQLite open 与测试注入点、`ensureSessionMetaDBUnsafe`、`openSessionMetaDB`、迁移/归一 helper、DB link 读写与 per-root user DB 路径原样迁至 `manager_sqlite.go`（297 行）。Schema SQL 常量、binding 事务和 row serialization 留在原处；父文件实测 2,174 行。
- **针对性测试**：`session_db_migration_order_test.go`、`manager_cursor_migration_test.go`、`manager_test.go`、`manager_pinned_at_retired_test.go` → 守住迁移先后、外部 binding 游标列、DB link fallback、alias/fork 归一及 pinned_at 退役语义。

### G-EQ Session manager list/meta 查询职责拆分

- **可见症状**：列表 API、meta-only 查询、binding 填充、archived 过滤、最新更新时间聚合和 SQL 条件拼装挤在 `manager.go`；维护列表时容易误触正文读取或改变 pinned/archived 过滤语义。
- **根因与边界**：`List`、`ListByKeys`、`Count`、`LatestSessionUpdatedAt`、`ListMetas` 与 meta query/filter helpers 原样迁至 `manager_session_list.go`（258 行）。Search 继续调用同包查询方法，SQL 条件、顺序、meta-only 行为不变；父文件实测 1,926 行。
- **针对性测试**：`manager_test.go`、`manager_latest_updated_at_test.go`、`manager_search_test.go`、`manager_pinned_at_retired_test.go` → 守住列表/搜索 query、最新时间聚合、置顶只读 meta、archived 默认过滤和 key 顺序。

### G-ER Session manager session alias 与 external-name 拆分

- **可见症状**：手动标题需在会话重命名、外部 transcript 重导和列表回填间保持一致；若 alias/external-name 存储或空 id 守卫有分叉，标题会丢失或多个未启动会话串名。
- **根因与边界**：session alias/external name upsert、agent session id 投影、alias 查找与批量/单条列表回填原样迁至 `manager_aliases.go`（188 行）。空 `agent_session_id` 仍被拒绝进入 external-name 映射，公开 API 与数据库结构不变；父文件实测 1,746 行。
- **针对性测试**：`manager_test.go`、`manager_rename_compat_test.go` 和 usecase 测试 → 守住改名、外部名回填、删除后重导兼容与空 id 隔离。

### G-ES Session manager metadata row 读写与归一职责拆分

- **可见症状**：同一个 session meta row 同时由 `manager.go` 负责读、合并 bindings、写入和字段归一；schema 兼容修复容易只改一侧，产生列表信息丢失或重启后数据形状不同。
- **根因与边界**：单行 metadata 查询、meta+bindings 投影、upsert、SQLite 参数序列化、scan、字段归一与共享 row scanner 原样迁至 `manager_meta.go`（264 行）。SQL schema 常量和缓存/transcript orchestration 不变；父文件实测 1,492 行，低于 1,500 硬线，继续朝 800 软线拆分。
- **针对性测试**：`manager_test.go`、`manager_pinned_at_retired_test.go`、`manager_rename_compat_test.go`、`session_db_migration_order_test.go` → 守住 meta 字段往返、related worktree/文件归一、pinned_at 退役和数据库迁移顺序。


### G-ET Session manager related-file 记录职责拆分

- **可见症状**：会话相关文件去重、移除、输出产物登记和父子会话传播规则混在 manager 主文件，文件关联语义难以单独审阅。
- **根因与边界**：`AddRelatedFile`、按 repo/head/path 移除、输出文件登记、父会话传播与身份/路径归一 helper 原样迁至 `manager_related_files.go`（156 行）；worktree 归属与清理方法留在原处，不涉及 repoint。父文件实测 1,345 行。
- **针对性测试**：`manager_test.go` → 守住 repo/head/path 范围去重与删除、子会话文件传播、fork 不向源会话传播；全仓 `make test` → 全仓 Go 测试门禁通过。


### G-EU Session manager exchange identity context contract 拆分

- **可见症状**：exchange 轮次身份、写入来源、模型显示名、token usage 的 context 传递与确定性 MindFS ID 生成/形状校验混在 manager 主文件，身份合同的读写约定难集中审阅。
- **根因与边界**：context keys、`ExchangeIdentity`、With/读取 helper、UUIDv5 `NewMindFSID`、user turn id 与形状校验原样迁至 `manager_exchange_identity.go`；公开符号、调用点及 exchange 写入/分配时序不变。父文件实测后续记入计划。
- **针对性测试**：`server/internal/api/usecase/session_blocks_write_test.go`、`server/internal/api/usecase/session_identity_dedup_test.go`、`server/internal/api/stream_hub_turn_identity_test.go`、`server/internal/session/manager_test.go` → 守住稳定 UUIDv5、轮次 identity 传递、live/import 来源以及模型与 usage 元数据。


### G-EV Session manager lifecycle operation split

- **可见症状**：会话关闭/删除、重命名、归档、模型与运行属性更新以及 DB shutdown 生命周期实现与缓存/transcript 编排混在 manager 主文件，修改元数据生命周期时难以独立追踪持久化和清理顺序。
- **根因与边界**：Close/Delete/Rename/Archive、模型/shell/plan/context-window 更新、内部 close/delete 与 Shutdown 原样迁至 `manager_lifecycle.go`（256 行）；worktree 归属和 repoint 实现不变。父文件实测 934 行。
- **针对性测试**：`server/internal/session/manager_test.go` → 守住会话创建、重命名、归档、属性更新、删除时主日志/aux/块流清理与关闭行为；全仓 `make test` → 全仓 Go 测试门禁通过。


### G-EW Session manager exchange window read split

- **可见症状**：窗口分页、冷缓存尾读、JSONL 记录计数、尾部解析与 lower-bound 切片共同驻留 manager 主文件；改分页边界时难对照冷/热缓存路径。
- **根因与边界**：窗口读取编排、内存切片、磁盘尾读、记录计数、尾部解析与 seq lower-bound helper 原样迁至 `manager_window_reads.go`（193 行）；缓存优先、冷读 fallback 与窗口定义保持。父文件实测 749 行。
- **针对性测试**：`manager_window_test.go`、`manager_window_cold_test.go`、`manager_blocks_window_test.go` → 守住冷热窗口结果一致、尾部读取不触发全量解析与块流窗口语义；全仓 `make test` → Go 门禁通过。

### G-EX API HTTP Git handler 域拆分

- **可见症状**：Git 状态/差异/历史、分支操作、提交/暂存/丢弃、related-file diff 与 worktree handler 挤在 3,024 行 `http.go` 中，HTTP 文件路由与跨域文件/会话逻辑混杂。
- **根因与边界**：Git handler 和仅供这些 handler 使用的请求解码/响应 helper 原样迁至 `http_git.go`（447 行）；`Routes` 内路由注册与 handler 名称不变，数据仍通过 usecase service 派发。父文件降至 2,588 行，仍需后续按域拆分。
- **针对性测试**：`server/internal/api/http_git_related_files_stats_test.go` → 守住 Git related-file stats 的请求校验和路由注册；`make test` → 全仓 Go 门禁。


### G-EY API HTTP file and managed-directory handlers split

- **可见症状**：tree/file/upload 与 managed directory 增删改、路径解析及响应投影和 session/Git handler 混在 general `http.go`；项目文件端点的请求校验与文件系统边界难单独检查。
- **根因与边界**：`handleTree`、`handleFile`、upload parser/closer、query parsers、directory handlers、managed-dir path/response helpers 原样迁至 `http_files.go`（574 行）；路由、校验顺序和 usecase dispatch 不变。父文件实测 2,036 行，仍高于硬线。
- **针对性测试**：`http_test.go`、`http_open_api_test.go`、`http_node_role_test.go` → 守住文件树/目录路由、无鉴权契约与 worker 数据面访问；`make test` → 全仓 Go 门禁。


### G-EZ API HTTP session handlers split (repoint excluded)

- **可见症状**：session list/search/import/read/sync/fork/name/pin/delete/archive handlers 与 session response/shell projection 混在 general `http.go`；修改同步或列表协议时难以集中审阅读写流程。
- **根因与边界**：除 `handleSessionRepoint` 外的 session handlers、session response/list response 与 shell/title projection helpers 原样迁至 `http_sessions.go`（1,184 行）；repoint handler 留在 `http.go` 且未改，路由与调用顺序保持。父文件实测 867 行。
- **针对性测试**：`http_window_test.go`、`http_session_message_test.go` → 守住窗口/session API 与消息路径；`session-window.test.mjs`、`conditional-request.test.mjs`、`user-message-persist-timing.test.mjs`、`session-archive.test.mjs` 与 `session-list-node-failure.test.mjs` → 守住 handler 窗口接线、条件响应、user 行落盘、归档解绑与列表 payload 语义；`make test` → 全仓 Go 门禁。


### G-FA API HTTP static frontend serving split

- **可见症状**：静态资源清理/缓存头、index 缺失资源检测、fallback HTML、版本标识与 worker frontend 403 guard 混在 general `http.go`，部署前端资源问题难与路由和 session handler 分开审阅。
- **根因与边界**：frontend/static handlers、index resource regex 与 cache/path/version/fallback helpers 原样迁至 `http_frontend.go`（218 行）；`/` 路由、静态资源可用角色判断和响应语义不变。父文件实测 658 行。
- **针对性测试**：`server/internal/api/http_test.go` → 守住静态路径清理、index resource 与 cache fallback 行为；`server/internal/api/http_node_role_test.go` → 守住 worker frontend 403；`make test` → 全仓 Go 门禁。


### G-FB Claude session model/config normalization split

- **可见症状**：Claude model alias、1m suffix 归一、settings/env 合并、CLI model arg 选择与 model listing 混在 session turn/event 主文件，模型配置变更难和会话生命周期逻辑区分。
- **根因与边界**：model alias/canonicalization、settings/env 解析、model arg 决策、隐藏模型过滤、model list/info/effort projection 原样迁至 `session_models.go`（406 行）；SDK 选项及 session 行为保持。父文件实测 2,682 行，仍高于硬线，继续拆分。
- **针对性测试**：`server/internal/agent/claude/session_test.go` 与 `web/tests/upstream-restore.test.mjs` → 守住 alias/第三方模型透传、1m suffix、env/settings precedence 与 model listing 及 upstream-restore 断言位置；`make test` → 全仓 Go 门禁。


### G-FC Claude session tool-call summary projection split

- **可见症状**：Claude tool name/input 归一、path/execute/search/todo/task/list/ask-user/web-search 摘要和内容片段投影与主 turn 消费循环共处大型 `session.go`，工具卡字段变化难以按 tool 家族集中审阅。
- **根因与边界**：`summarizeToolCall` 至 `cloneToolMeta` 的 tool-call summary/metadata projection helpers 原样迁至 `session_tool_summaries.go`（469 行）；调用时机、fallback metadata 和输出结构不变。父文件实测 2,222 行，仍高于硬线。
- **针对性测试**：`server/internal/agent/claude/session_test.go` → 守住 execute 描述/title 回退、task/result summary 与 ask-user 结构；`make test` → 全仓 Go 门禁。


### G-FD Claude session tool-result lifecycle and projection split

- **可见症状**：Claude pending tool-call 状态迁移、task tool 结果关联与 stdout/stderr/content 归一散落在大型 session event 文件中；修改结果映射时难以集中核对锁与状态契约。
- **根因与边界**：pending tool-call 生命周期和 tool-result matching/normalization/projection helpers 原样迁至 `session_tool_results.go`（486 行）；锁范围、pending 消费时序、task list 状态映射与输出结构不变。父文件实测 1,746 行，仍高于硬线。
- **针对性测试**：`server/internal/agent/claude/session_test.go` → 守住 pending tool-call 生命周期、TaskCreate/Update/List/Get 结果更新与通用结果摘要；`make test` → 全仓 Go 门禁。


### G-FE Claude SDK message handling and subagent projection split

- **可见症状**：Claude SDK partial/assistant/user/task/compact/auth 消息处理、ask-user 交互和 subagent/task 事件投影集中在 session 主文件，事件契约与会话主循环难以分开审阅。
- **根因与边界**：`handlePartialAssistantMessage` 至 `taskType` 的消息处理、ask-user 与 subagent/task projection 连续职责段原样迁至 `session_messages.go`（684 行）；调用顺序、事件类型、锁和会话状态语义不变。父文件实测 1,076 行，低于硬线。
- **针对性测试**：`server/internal/agent/claude/session_test.go` → 守住 SDK 消息到 MindFS event 的转换、ask-user 与 subagent/task 状态映射；`make test` → 全仓 Go 门禁。


### G-FG Agent API provider configuration file helpers split

- **可见症状**：API provider 写入 agent 配置时，JSON-like 解析、YAML/JSON 文件 I/O、模型表结构与 managed block 操作混在 provider handler 和 agent-specific apply 逻辑中，配置格式维护难以聚焦。
- **根因与边界**：文件与配置映射 helpers 从 `firstModelOrDefault` 起原样迁至 `agent_api_provider_files.go`（449 行）；序列化格式、注释/引号/尾逗号归一和 managed sequence 语义不变。父 provider 文件实测 2,047 行，后续继续拆分。
- **针对性测试**：`server/internal/api/agent_api_provider_test.go` 与 `server/internal/api/agent_api_provider_sync_test.go` → 守住 Codex TOML 与 provider 写入/同步、文件配置更新及模型投影；`make test` → 全仓 Go 门禁。


### G-GH Agent API provider config application adapters split

- **可见症状**：provider 的 apply orchestration、Codex TOML 合并及多个 agent 配置写入适配集中在 API/provider 入口文件，扩展一种 agent 配置格式容易影响其他协议路径。
- **根因与边界**：`applyAgentAPIProvider` 至 `probeAgentAPIProvider` 前的通用 apply、Codex TOML 合并、各 agent adapter 与 env 更新逻辑原样迁至 `agent_api_provider_apply.go`（769 行）；provider 选择、写入顺序、agent-specific env 语义不变。父文件实测 1,291 行。
- **针对性测试**：`server/internal/api/agent_api_provider_test.go`、`server/internal/api/agent_config_test.go` 与 `server/internal/api/agent_api_provider_sync_test.go` → 守住协议应用、现有配置保留、env 清理/合并与批量同步；`make test` → 全仓 Go 门禁。


### G-GI Kanban task stage execution split

- **可见症状**：任务阶段调度、agent 执行、prompt 值生成及成功/失败收尾与任务 CRUD、状态迁移和存储编排挤在同一 service 文件，修改阶段执行行为容易误触其他流程。
- **根因与边界**：`executeTask` 至 `recordTaskError` 的 stage execution、agent dispatch、prompt projection 与 run completion/error persistence 原样迁至 `service_execution.go`（360 行）；执行顺序、状态/event、失败记录与 worktree/repoint 生命周期保持原位。父文件实测 1,640 行，后续继续拆分。
- **针对性测试**：`server/internal/kanban/service_test.go` 与 `server/internal/kanban/worktree_finish_stage_test.go` → 守住阶段执行顺序、提示词和失败/等待状态；`make test` → 全仓 Go 门禁。


### G-GJ Kanban task template and query service split

- **可见症状**：阶段/任务模板 CRUD、任务创建与快照、列表/详情/跨项目 overview 混在状态转换和执行服务中，模板变更与任务查询难以独立审阅。
- **根因与边界**：`ListStageTemplates` 至 `GetTask` 的模板、创建与读取职责原样迁至 `service_tasks.go`（228 行）；模板快照、任务编号、overview 单项目失败跳过和排序语义不变。父 `service.go` 实测 1,422 行，低于硬线。
- **针对性测试**：`server/internal/kanban/service_test.go` → 守住模板 CRUD、创建快照、任务列表/详情/overview；`web/tests/task-panel-unification.test.mjs` 与 `web/tests/workspace-node-fanout.test.mjs` → Go 源守卫同时检查 facade 与 task/query 模块；`make test` → 全仓 Go 门禁。


### G-GK Codex importer tool-call projection split

- **可见症状**：历史 Codex JSONL 导入中的 tool-call 输入识别、JavaScript wrapper 解析、patch/location 与 tool output 投影占据 importer 主文件大段代码，使 transcript scanning/import orchestration 难以审阅。
- **根因与边界**：`parseImportedCodexToolCall` 至 `importedCodexAssistantLine` 的工具调用解析和输入/输出投影原样迁至 `importer_tool_calls.go`（850 行）；导入 exchange 输出和 tool matching 行为不变。父 `importer.go` 实测 1,047 行，低于硬线。
- **针对性测试**：`server/internal/agent/codex/importer_test.go` → 守住工具识别、命令/patch 解析、工具结果与 assistant/user 输出；`make test` → 全仓 Go 门禁。


### G-GL Codex session tool item projection split

- **可见症状**：Codex session SDK tool item、dynamic/hook/image/collaboration 事件映射与内容/title 投影挤在会话事件消费文件中，新增工具类型容易影响 session turn 逻辑。
- **根因与边界**：`mapToolItem` 至 `truncateRunes` 的 tool item mapping 和内容/title helpers 原样迁至 `session_tools.go`（433 行）；调用方、工具种类/status 与 event payload shape 不变。父 `session.go` 实测 1,164 行，低于硬线。
- **针对性测试**：`server/internal/agent/codex/session_test.go` → 守住工具 item 映射、动态工具、hook/image/collaboration 内容与 status；`make test` → 全仓 Go 门禁。


### G-GM Git view diff and history read model split

- **可见症状**：diff、commit history、related-file diff/stat 与 status/branch/worktree/action handlers 共处大型 gitview 文件，历史解析调整容易误触仓库状态和写操作。
- **根因与边界**：`ReadDiff` 至 `ReadRelatedFileStats` 以及 `repoContext.commitExists` 至 `parseHistoryItems` 的只读 diff/history 实现与解析 helper 原样迁至 `gitview_history.go`（653 行）；路径映射、历史排序、commit/diff 选择和输出 shape 不变。父文件实测 933 行。
- **针对性测试**：`server/internal/gitview/gitview_test.go` → 守住 diff/history/related-file diff 与 batch stats；`make test` → 全仓 Go 门禁。

### G-GN Release and development entrypoint contract

- **可见症状**：启动配置若只修在 `server/cmd/mindfs-server`，dev-backend 的测试会绿，但发布的 mindfs 二进制仍不生效。
- **根因与边界**：明确 `make build` / `scripts/build-all.sh` 使用 `cli/cmd`，`make dev-backend` 使用 `server/cmd/mindfs-server`；新增 CLI 包测试读取 Makefile 与 build-all 脚本验证入口分工，缺省配置语义仍由 startup config tests 守住。
- **针对性测试**：`cli/cmd/startup_config_test.go:TestReleaseAndDevelopmentEntrypointsStaySeparated` → 锁定发布/开发入口，其他 startup config tests → 锁定配置行为；`make test` → 全仓 Go 门禁。

### G-GO WebSocket request handler async dispatch

- **可见症状**：单读 goroutine 同步调用 plan-mode 和 queue handler 时，session message 读取会等待其 storage/session work；后续 stop/queue 请求被堵在同一连接。
- **根因与边界**：`handleWSRequest` 中原同步的 ping、plan-mode、queue remove/update/send-now handler 改为 goroutine dispatch；既有 `session.cancel` 同步取 active turn id 后异步执行的顺序保持。handler 的响应与业务行为不变。
- **针对性测试**：`server/internal/api/ws_test.go:TestWSRequestHandlersAreDispatchedAsynchronously` → AST 检查 dispatch 内所有 `h.handle*` 都在 goroutine 中；`go test ./server/internal/api/...` 与 `make test` → 全仓 Go 门禁。

### G-GP Agent documentation entry and plan link checks

- **可见症状**：规则/计划入口、相对链接或 Claude/Gemini 工具导入目标失效时，agent 仍可能按错误规范或过期计划工作。
- **根因与边界**：`agent-docs-contract.test.mjs` 验证 AGENTS/规则目录/计划索引的本地 Markdown 链接、最多一个 Active 计划（无执行中任务时允许为零），以及 Claude/Gemini 对通用入口的导入关系；只校验存在性和入口契约，不验证外部链接可达性。
- **针对性测试**：`web/tests/agent-docs-contract.test.mjs` → 入口链接、Active 计划数量和工具导入三项契约；`npm test` → web 全量门禁。

### G-GQ Frontend reviewer architecture guards

- **可见症状**：组件层重新直接依赖 service，或前端源码文件持续膨胀时，人工 review 可能漏掉回归。
- **根因与边界**：`reviewer-architecture-rules.test.mjs` 扫描物理源码，要求组件到 service 的依赖边为零，并要求所有前端源码遵守统一硬上限；不再使用历史边 allowlist 或路径豁免。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住零组件/service 依赖边与统一文件大小要求；`npm test` → Vitest 前端门禁。

### G-GR Backend reviewer rule guards

- **可见症状**：API handler 直接构造账户级 `AppContext`，或 Go 源码文件持续膨胀时，reviewer checklist 可能无法阻止回归。
- **根因与边界**：`workspace_test.go` 禁止 handler 直接构造 `AppContext`，并对 `cli/` 与 `server/` 下所有 Go 文件应用同一大小上限，包括测试与生成文件；不设路径或文件类型豁免。账户工作区行为不变。
- **针对性测试**：`server/internal/api/workspace_test.go` 中的 AppContext 构造与 Go 文件大小守卫 → 守住统一约束；`make test` → 全仓 Go 门禁。

### G-GS Shared candidate contracts

- **可见症状**：候选项仅用于前端展示与选择，但组件从 `services/candidates` 导入类型，导致 UI 层对数据请求服务产生不必要的结构依赖。
- **根因与边界**：候选项联合类型和 `CandidateItem` 移至 `web/src/shared/candidateTypes.ts`；service 从 shared 导入并继续 re-export 以保持现有非组件消费者兼容。三个组件改为从 shared 导入，既有请求与选择行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 禁止组件到 service 导入边新增并验证 allowlist 只减；`npm run typecheck` → 守住 TS 类型兼容。

### G-GT Shared agent status contracts in views

- **可见症状**：Agent 状态展示组件只需要读取 `AgentStatus`，却通过 `services/agents` 类型 facade 形成 UI 到运行时请求层的依赖。
- **根因与边界**：`AgentMenuList`、`AgentSelectorErrorBanner`、`AgentSelectorMenuHeader` 改从既有 `shared/agentTypes.ts` 导入 `AgentStatus`；service 仍负责请求，组件行为不变，allowlist 删除三条依赖边。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-GU Agent status types for editor and task views

- **可见症状**：Agent selector、prompt editor 与阶段编辑器仅读取 Agent 状态，却从请求 service 导入类型。
- **根因与边界**：三个组件直接引用 `shared/agentTypes.ts`；service 继续负责获取/重启行为，运行时语义不变，allowlist 再减少三条边。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-GV Agent status types in task panels

- **可见症状**：任务详情与模板对话框仍从 agent 请求 service 导入 AgentStatus，虽然这两个组件只需展示层类型。
- **根因与边界**：两个组件改从 `shared/agentTypes.ts` 导入类型；task service 的运行时操作保持原路径与行为，allowlist 删除两条 agent service 边。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-GW Agent status types in action and session views

- **可见症状**：ActionBar 和 session 时间线展示依赖 agent 状态类型，却从运行时 agent service 导入。
- **根因与边界**：三个组件改从 `shared/agentTypes.ts` 导入 `AgentStatus`；仍需的 prompts/session/error/clipboard/node 服务边不变，只删掉 agent 类型依赖。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-GX Shared task contracts in read-only views

- **可见症状**：三个任务展示菜单/视图仅使用 `KanbanTask` 或 `TaskTemplate` 类型，却依赖 task service facade。
- **根因与边界**：组件改从既有 `shared/taskTypes.ts` 导入纯类型；任务请求与业务操作仍由 task service 提供，展示行为不变，allowlist 删除三条边。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-GY Shared task contracts in workspace and detail views

- **可见症状**：任务详情及工作台行/快捷启动组件仅需任务结构类型，却直接依赖 task service facade。
- **根因与边界**：三个组件从既有 `shared/taskTypes.ts` 导入纯类型，TaskDetailPanel、WorkspaceTaskRow、WorkspaceQuickLaunch 不再依赖任何 service；数据请求仍留在 app/usecase 层，显示与请求行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-GZ Shared task contracts in editors and workspace board

- **可见症状**：PromptEditor、StageEditor 与 WorkspaceBoard 只使用任务类型，但组件仍通过 task service facade 取得这些类型。
- **根因与边界**：三者改从 `shared/taskTypes.ts` 导入；实际任务 CRUD/运行继续留在 service/app 层，组件展示和编辑流程不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-HA Shared Git response contracts

- **可见症状**：Git 视图只消费后端响应类型，却通过 `services/git` 导入类型，表现层因此依赖请求门面。
- **根因与边界**：纯响应契约集中到 `shared/gitTypes.ts`；service 类型模块继续 re-export 兼容旧消费者，`GitDiffPayload` 仍在 service 层组合缓存 payload。三个展示组件改从 shared 导入，Git 请求和缓存行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 service facade 与 shared 类型兼容。

### G-HB Shared directory view contracts

- **可见症状**：图标与 Git 项目视图只引用 `FileEntry`、`DirectorySortMode` 类型，却从目录排序 service 导入。
- **根因与边界**：三个组件改从既有 `shared/directorySort.ts` 导入类型；目录排序常量与运行逻辑仍留在 service，视图行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型解析。

### G-HC Shared session domain contracts

- **可见症状**：session 视图组件需要 `Session`、`ToolCall`、`RelatedFile` 等纯领域类型，却从 session runtime facade 导入；类型模块内部又被多个实现文件共享。
- **根因与边界**：完整 `sessionTypes.ts` 搬至 `shared/sessionTypes.ts`；原 service 内部路径留兼容 re-export，现有公开 service facade 的导出面与运行时行为不变。纯类型组件改用 shared，混合 runtime 组件只切换类型导入。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`web/tests/message-mechanism-merges.test.mjs` 与 `web/tests/session-window.test.mjs` → 从新 shared 路径验证身份字段和窗口契约；`npm run typecheck` 与 `npm test` → 守住类型解析、service facade 和前端兼容测试。

### G-HD Git views use shared response types

- **可见症状**：根项目 Git/worktree 视图依赖 `services/git` 仅为取得响应类型，因此纯展示组件仍连接请求模块。
- **根因与边界**：GitStatusPanel、RootGitContentView、RootWorktreeContentView 改用 `shared/gitTypes.ts`；GitStatusPanel 的 dialog 操作依赖保留，其他 Git 请求与状态行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 shared 类型与 service re-export 兼容。

### G-HE Shared upload progress contract

- **可见症状**：上传进度组件只展示 `UploadProgress`，却从上传请求 service 导入类型。
- **根因与边界**：上传进度契约移至 `shared/uploadTypes.ts`；upload service 继续 re-export 并使用此类型，XHR 上传和进度计算不变，两个展示组件移除 service 依赖。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住上传类型 facade。

### G-HF Shared shell status contract

- **可见症状**：shell 选择器只需要显示 shell 选项，却从 agent service 导入类型。
- **根因与边界**：`ShellStatus` 并入既有 `shared/agentTypes.ts`；agent service 兼容 re-export，shell discovery 和选择行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 service 类型兼容。

### G-HG Shared file and editor contracts

- **可见症状**：文件查看与编辑器视图依赖 `services/file`、`services/fileEditing` 取得 `FilePayload`、编辑会话和编辑 store 交互类型。
- **根因与边界**：数据与编辑状态契约移入 `shared/fileTypes.ts`；service 保持 `FilePayload`、编辑类型 re-export，`FileEditStore` 类继续实现共享 contract。文件获取、编辑、缓存和保存流程不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` 与 `npm test` → 守住类型、文件编辑以及兼容路径测试。

### G-HH Shared document preview contracts

- **可见症状**：文档查看组件只需预览种类和 PDF 页面布局类型，却依赖 document preview service。
- **根因与边界**：两个类型移入 `shared/documentPreviewTypes.ts`，service 保持 re-export；PDF 页码范围计算和实际预览路径不变，DocumentViewer 移除 service 类型导入。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型 facade。

### G-HI Directory sort view imports shared module

- **可见症状**：DirectorySortMenu 的排序选项与模式类型已由 shared 持有，组件却仍绕道 prefs service facade。
- **根因与边界**：组件改从 `shared/directorySort.ts` 导入常量与类型；service facade 和排序行为未改。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住组件与共享契约。

### G-HJ Clipboard capability under platform

- **可见症状**：Markdown/session views 与 app 调用方为复制文本而导入 services/platform，虽该模块实现的是浏览器/Native clipboard capability 而非数据 service。
- **根因与边界**：实现迁到 `web/src/platform/clipboard.ts`；旧 `services/platform/clipboard.ts` 仅 re-export 兼容，Native bridge、Capacitor、execCommand 与 navigator fallback 顺序不变。组件和 app 调用方改用 platform 入口。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 阻止组件 service 边增长；`npm test` → 守住旧路径兼容及复制调用行为。

### G-HK Shared session duration helpers

- **可见症状**：SessionViewer 和 Timeline 为显示单轮耗时而直接导入 session service 子模块。
- **根因与边界**：纯时间戳归一与耗时格式化 helpers 移到 `shared/sessionDuration.ts`；旧 service 子路径兼容 re-export，流时间语义与标签格式不变，源码测试指向新的权威定义。
- **针对性测试**：`web/tests/session-duration.test.mjs` → 守住时长阈值、无效时间处理与服务端事件时间优先；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增。

### G-HL Shared session group display resolver

- **可见症状**：MultiProjectSessionList 为显示节点色而直接导入 session service 子模块中的纯函数。
- **根因与边界**：纯 `resolveGroupColor` 及其数据契约移到 `shared/sessionGroupDisplay.ts`；旧 service 子路径保留兼容 re-export，节点色解析优先级不变。MultiProjectSessionList 改从 shared 导入，相关 app/hook 调用与源码测试也指向 shared。
- **针对性测试**：`web/tests/session-group-display.test.mjs` → 守住复合键、节点 id/name 回退顺序；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住兼容 facade 与 shared 契约。

### G-HM Shared session activity timestamp resolver

- **可见症状**：SessionCard 只需选取会话列表的活动时间戳，却直接导入 session merge service。
- **根因与边界**：纯 `sessionActivityAt` resolver 移到 `shared/sessionActivityAt.ts`；session merge service 兼容 re-export，时间戳比较及空值行为不变。SessionCard 改用 shared 入口，源码测试直接加载权威实现。
- **针对性测试**：`web/tests/session-list-merge.test.mjs` → 守住 activity_at 与 updated_at 的优先级；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住类型与 facade。

### G-HN Shared session tree utilities

- **可见症状**：SessionList 为清理已删除会话的子树展开态而直接依赖 session service 子模块中的纯计算；应用 hooks 也绕经该 service 路径调用纯树工具。
- **根因与边界**：`collectSessionSubtreeKeys`、`pruneChildState` 与数据契约整体迁至 `shared/sessionTree.ts`；旧 service 子路径只保留兼容 re-export。应用 hooks 与列表组件改用 shared，子树级联与展开态回收语义不变。
- **针对性测试**：`web/tests/session-tree.test.mjs` → 守住子树收集、循环保护与失效展开态回收；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 service facade 与 shared 契约。

### G-HO Components import shared Git and upload contracts

- **可见症状**：SessionMainContent 与 DefaultListView 只需要 Git 状态和上传进度类型，却分别从 Git/upload service 导入这些契约。
- **根因与边界**：两个展示组件改从既有 `shared/gitTypes.ts` 与 `shared/uploadTypes.ts` 导入类型；service 与 Git runtime imports、Git 请求、上传行为均不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住共享契约与组件类型。

### G-HP Candidate prompt actions move to hook

- **可见症状**：CandidateDropdown 同时渲染候选列表并直接管理 prompt 保存/删除请求、请求状态及错误文案。
- **根因与边界**：保存/删除状态和请求流程移入 `useCandidatePromptActions`；组件保留表单与候选 UI。添加/删除各自的错误文案、保存成功与删除成功回调、取消时恢复焦点的时机保持不变。
- **针对性测试**：`web/tests/candidate-prompt-actions-contract.test.mjs` → 守住请求和状态归 hook、成功回调及双错误文案；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住组件/hook 契约。

### G-HQ Shared PDF visible page calculation

- **可见症状**：PdfPreview 为计算虚拟化页面区间而依赖 document preview service。
- **根因与边界**：纯 `visiblePageRange` 与 `PDF_RENDER_MARGIN_PX` 移至 `shared/documentPreview.ts`；service 保留兼容 re-export，文档种类识别、渲染窗口与页码回退行为不变。
- **针对性测试**：`web/tests/document-preview.test.mjs` → 守住 PDF 页面区间计算边界；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 shared 类型。

### G-HR Components import shared default node color

- **可见症状**：ActionBar、ModeIcon 与 ModeSelector 只为 fallback 色值导入 nodeRegistry runtime service。
- **根因与边界**：三处 `DEFAULT_NODE_COLOR` 改从既有 `shared/nodePalette.ts` 导入；调色板定义与颜色选择行为不变，仍需 node registry 的组件不受影响。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 palette 导出契约。

### G-HS Protected image fetches move to hook

- **可见症状**：MarkdownImage 与 ImageViewer 将视图渲染和 proof-protected blob 请求、取消及 object URL 清理耦合在组件中。
- **根因与边界**：请求与 object URL 生命周期移入 `useProtectedBlobUrl`，两个组件通过 hook 取得 URL；Markdown 直接图片/相对路径解析、失败占位和 URL 回收行为保持不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`web/tests/markdown-viewer-split.test.mjs` → 守住 MarkdownImage 在 viewer 链中的接线；`npm run typecheck` → 守住 hook 调用契约。

### G-HT Remove unused SessionViewer prompt import

- **可见症状**：SessionViewer 保留一个从未使用的 prompts service import，制造虚假的组件 service 依赖边。
- **根因与边界**：删除未使用 import 与 reviewed debt allowlist 项；SessionViewerTimeline 仍负责实际传递保存函数，运行行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住实际引用完整性。

### G-HU Multi-project list receives node snapshot from hook

- **可见症状**：MultiProjectSessionList 为渲染项目节点色直接调用 nodeRegistry，而其关联逻辑 hook 已负责排序和读取节点列表。
- **根因与边界**：node snapshot 由 `useMultiProjectSessionList` 返回给组件；组件继续用 shared palette 与纯 group color resolver，节点排序和颜色回退行为保持一致。
- **针对性测试**：`web/tests/session-list-merge.test.mjs` → 守住逻辑 hook 的节点快照接线与项目排序；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 hook 返回契约。

### G-HV Default list imports shared directory sorting

- **可见症状**：DefaultListView 为排序目录项和读取排序选项而通过 prefs service facade 导入纯展示排序能力。
- **根因与边界**：排序函数、选项与目录项契约直接从既有 `shared/directorySort.ts` 导入；兼容 service facade 保留，排序模式与结果不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` → 守住 shared 导出契约。

### G-HW Toast error subscription moves to hook

- **可见症状**：ToastContainer 同时订阅 error service、管理自动过期和 retry 状态，再渲染提示 UI。
- **根因与边界**：订阅、提示队列、过期计时与 retry 流程迁至 `useToastErrors`；`AppError` 契约归 `shared/errorTypes.ts` 并由旧 error service re-export。提示级别过滤、错误/普通提示时长、重试与视觉样式保持不变。
- **针对性测试**：`web/tests/toast-errors-hook.test.mjs` → 守住订阅过滤、时长、过期和 retry；`web/tests/task-card-parity.test.mjs` → 守住 Toast 视觉契约；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增。

### G-HX Dialog host integration moves to hook

- **可见症状**：DialogHost 同时订阅 dialog service、维护请求/输入状态、处理返回键与全局键盘事件，再渲染弹窗。
- **根因与边界**：服务状态、取消/确认桥接、返回层注册及键盘处理迁至 `useDialogHost`；组件保留输入焦点和视图渲染，Esc/点外取消、Enter 确认、prompt 返回值语义不变。
- **针对性测试**：`web/tests/dialog-host-hook.test.mjs` 与 `web/tests/back-navigation.test.mjs` → 守住服务桥接和顶层返回语义；`web/tests/dialog-lifecycle.test.mjs` → 守住取消/确认结算；架构门禁与 typecheck 守住分层及契约。

### G-HY Session viewer error reporting uses hook

- **可见症状**：SessionViewerTimeline 与 SessionViewerSlashCommand 将全局错误 service 直接带进消息展示/交互组件；SessionViewer 还保留未使用的 error import。
- **根因与边界**：通用错误报告适配放进 `useReportError` hook，两个实际调用组件通过 hook 调用；删除未使用 import。错误码、翻译文案与全局错误分发不变。
- **针对性测试**：`web/tests/report-error-hook.test.mjs` → 守住两个组件经 hook 报错且不直接依赖 service；架构门禁与 `npm run typecheck` 守住层级和契约。

### G-HZ File operation flows move to hooks

- **可见症状**：FileOperations 同时负责菜单渲染、delete/rename dialog 与请求、move 目录浏览/请求、请求忙态及冲突错误映射。
- **根因与边界**：delete/rename 流程迁入 `useFileOperationActions`，move 浏览和提交迁入 `useFileOperationMove`；组件只编排菜单与 LocalPanel。非法文件名、确认顺序、并发/过期浏览响应保护、409 冲突文案和完成回调不变。
- **针对性测试**：`web/tests/file-operation-hooks.test.mjs` → 守住服务调用归 hooks、move 请求和竞态 guard；架构门禁与 `npm run typecheck` 守住组件边界及 hook 参数。

### G-IB Panel shell close guard moves to hook

- **可见症状**：PanelShell 在共用遮罩/卡片渲染组件中直接调用 dialog service 执行未保存修改确认。
- **根因与边界**：关闭守卫与放弃确认迁到 `usePanelShellClose`；PanelShell 仍处理遮罩点击和 UI。无脏内容直接关闭、取消继续编辑、确认后关闭的次序不变。
- **针对性测试**：`web/tests/task-panel-unification.test.mjs` → 守住三类关闭结果和统一 dirty guard；`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增。

### G-IC Remove dead onboarding service imports

- **可见症状**：OnboardingGate 留有三个未被使用的 service imports；完成/关闭动作实际都由同名 props 提供。
- **根因与边界**：删除死 import 与 reviewed allowlist 项；OnboardingTour 调用的 props、步骤和关闭行为不变。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住只减门禁；`npm run typecheck` → 确认 props 调用链仍完整。

### G-ID Task template save moves to hook

- **可见症状**：TaskTemplateDialog 同时维护表单草稿和模板保存请求、模板合法性校验及全局错误报告。
- **根因与边界**：保存与错误处理迁入 `useTaskTemplateSave`，组件仍负责编辑草稿和表单展示。首段 user 校验、项目 scope、归一阶段、错误文案及保存成功后回调/关闭顺序保持不变。
- **针对性测试**：`web/tests/task-template-save-hook.test.mjs` → 守住校验、scope payload 与成功回调顺序；架构门禁与 typecheck 守住组件边界和类型。

### G-IE Auth gate behavior moves to colocated hook

- **可见症状**：AuthGate 组件直接访问认证服务并维护请求状态、竞态保护和登录错误转换。
- **根因与边界**：页面级登录门帘的状态、请求与登录副作用迁入 `useAuthGate`；视图继续渲染 checking/locked/open 三态和登录表单。登录仍是页面门帘，API 保持公开。
- **针对性测试**：`web/tests/auth-gate-hook.test.mjs` → 守住现有账户放行、状态检查、卸载保护和错误码映射；架构门禁确认组件 service 边减少。

### G-IF Composer root node lookup moves to hook

- **可见症状**：ComposerTopChips 在 JSX 组件内直接读取全局项目节点映射，耦合 node registry service。
- **根因与边界**：root→node 查询封装进 colocated `useRootNodeId`；每次渲染仍读取当前映射，确保 managed root realtime 更新能刷新传给 Codex 指示器的 node id。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边只减不增；`npm run typecheck` 与前端构建守住 hook 使用契约。

### G-IG Account panel mutations use colocated hook

- **可见症状**：AccountPanel 的视图直接调用账户删除/设主 API、dialog service 和认证 service。
- **根因与边界**：删除确认与账户变更请求交给已有 `useAccountPanel`；成功后的列表刷新、错误显示和 busy 状态继续复用同一 `run` 流程；退出仍清除登录态并立即刷新页面。视图只触发 hook action。
- **针对性测试**：`web/tests/account-panel-actions.test.mjs` → 守住视图不直接调用三类 service、变更后刷新；账户相关现有用例、架构门禁与 typecheck 守住数据契约。

### G-IH ActionBar agent restart flows through logic hook

- **可见症状**：ActionBarControls 在 AgentSelector 回调里直接调用重启和 agents 查询 service，并自行写入 agent 状态。
- **根因与边界**：重启/刷新顺序迁入 `useActionBar` 的 handler，通过 ActionBar callback 传递；仍按当前项目解析 node id，重启成功后强制刷新 agent 列表再更新状态。
- **针对性测试**：`web/tests/claude-model-1m.test.mjs` → 守住 ActionBarControls 的模型行为未变；`web/tests/reviewer-architecture-rules.test.mjs` → 守住两条 service 边移除；typecheck 守住新回调契约。

### G-II Remove dead SessionMainContent service imports

- **可见症状**：SessionMainContent 带有多组从 App 机械拆分遗留、只在 import 行出现的 service 依赖。
- **根因与边界**：删除未使用的 file/fileEditing/git operation/directorySort/upload imports；仍使用的目录项、排序、上传和 Git 状态类型从 shared 导入，`GitDiffPayload` 保留在 git service types。渲染与操作回调不变。
- **针对性测试**：`web/tests/session-pane-split.test.mjs` → 守住视图拆分合同；`web/tests/reviewer-architecture-rules.test.mjs` → 守住删除的四条边不回流；typecheck 守住类型入口。

### G-IJ Remove dead AppActionBar service imports

- **可见症状**：AppActionBar 仍从 session service 导入一组不参与视图的缓存/同步 API，并从 sendShortcut service 导入未使用的读写函数。
- **根因与边界**：这些 imports 是 App 拆分遗留；删除死 service API imports，`QueuedUserMessage`、`SendShortcut` 类型改从 shared 模块导入。ActionBar props 和快捷键运行时归属不变。
- **针对性测试**：`web/tests/agent-docs-contract.test.mjs` → 入口/索引仍一致；架构门禁守住组件 service 边减少；typecheck 守住公开 props 类型。

### G-IK Remove dead FileTreePanel service imports

- **可见症状**：FileTreePanel 带有未使用的 file API、偏好读写函数和目录排序 service imports；运行状态本身由 props 提供。
- **根因与边界**：删除死 service imports，`FontSizePreferences`、`SendShortcut`、`DirectorySortMode`、`FileEntry` 均从已有 shared 类型模块导入。实际状态、回调与面板 UI 不变。
- **针对性测试**：`web/tests/file-tree-header-overlays.test.mjs` 与 `web/tests/file-tree-service-boundaries.test.mjs` → 守住 FileTree 呈现/分层；font-size/send-shortcut 现有测试及 typecheck 守住契约；架构门禁守住 service 边只减。

### G-IL Agent icon asset loading moves to hook

- **可见症状**：AgentIcon view 直接从 net service 取部署前缀 URL，并混合维护 SVG fetch/cache 生命周期；切到远端节点时 claude 等图标 403 丢图。
- **根因与边界**：资产路径解析、请求去重缓存、SVG data URL 转换、失败回退和卸载取消迁至 colocated `useCachedAgentIcon`；view 继续根据 agent 名称选择图标和展示尺寸。子路径部署 URL 语义保持。**静态资源只在控制面（页面服务器）存在**：图标 URL 必须用 `pageServerPath` 打页面服务器，不能用 `appPath` 跟随选中节点（worker 按设计不服务静态资源，直接 403）。
- **针对性测试**：`web/tests/agent-icon-hook.test.mjs` → 守住前缀解析、SVG 加载/回退及 view/service 边界；typecheck 与完整 build 守住 React hook 接线。

### G-IM Remove stale service imports from task/session panels

- **可见症状**：SessionSheet、TaskDetailPanelGate 与 TaskInlineEditPanel 从 service 导入的请求和缓存函数只出现在 import 声明里，业务数据由 props 提供。
- **根因与边界**：删除未使用的 agents/tasks/candidates/git/upload service API；仍被 props 使用的类型改从 `shared/` 契约导入。面板数据、回调与运行行为不变。
- **针对性测试**：`web/tests/task-panel-service-boundaries.test.mjs` → 守住三处视图只依赖 props/shared；`web/tests/task-panel-unification.test.mjs` 与 `web/tests/task-related-files.test.mjs` → 守住面板行为；架构门禁和 typecheck 守住边界。

### G-IN Session ring quick switch uses colocated hook

- **可见症状**：SessionRing 直接调用 session service 拉跨项目会话，并在 view 中组织请求状态；同时直接导入 quick switch service helper。
- **根因与边界**：快速切换数据加载及取消竞态迁入 `useSessionRingQuickSwitch`；hook 适配现有 `recentQuickSwitchGroups` 和 `ringGesture`，Session 类型从 shared 导入。定位、外部关闭、Esc 和拖拽行为不变。
- **针对性测试**：`web/tests/session-ring-gesture.test.mjs` → 守住手势归属、阈值 helper 和回调链；`web/tests/quick-switch.test.mjs` → 守住列表投影；架构门禁与 typecheck 守住 view/service 边界。

### G-IO Git status discard confirmation uses logic hook

- **可见症状**：GitStatusPanel view 直接调 dialog service 确认删除未跟踪文件，再自行编排 action busy 和 discard 回调。
- **根因与边界**：确认与 discard action 编排迁入已有 `useGitStatusPanel`；已跟踪文件仍直接 discard，未跟踪文件确认后才执行，取消不执行；按钮呈现不变。
- **针对性测试**：`web/tests/git-status-discard-hook.test.mjs` → 守住确认条件、action key 与 service 边界；架构门禁和 typecheck 守住 hook 接线。

### G-IP Session timeline service actions use colocated hook

- **可见症状**：SessionViewerTimeline 直接导入 prompt 保存和 persisted sequence 判定 service，同时负责向消息 presenter 传递这两项能力。
- **根因与边界**：通过 `useSessionViewerTimelineActions` 提供既有 service 函数；时间线投影、保存成功/失败提示和 fork 按钮资格保持不变。
- **针对性测试**：`web/tests/session-viewer-timeline-actions.test.mjs` → 守住 view 不直接依赖 services、hook 使用原服务实现；`web/tests/session-timeline-seq.test.mjs` → 守住持久消息判定；架构门禁守住边数。

### G-IQ Session attachment images reuse protected blob hook

- **可见症状**：AttachmentImage 自己重复实现受保护 blob 请求、node registry 查询和 object URL 生命周期。
- **根因与边界**：改用既有 `useProtectedBlobUrl` 统一请求和 URL 生命周期；hook 支持可选 nodeId，文件服务继续按 root registry 自动补全节点。图片显示、失败空态、卸载取消和 URL revoke 语义保持。
- **针对性测试**：`web/tests/markdown-viewer-split.test.mjs` → 守住 protected blob hook 的请求/清理；架构门禁守住组件不再直连 file/node service；typecheck 守住 hook 参数。

### G-IR Remove stale FileViewer editing service import

- **可见症状**：FileViewer 从 fileEditing service 导入 store、key builder 和大小限制，但这些符号都只出现于 import 声明；组件通过 `useFileViewer` 获取实际编辑状态。
- **根因与边界**：删除机械拆分遗留的死 import；编辑能力、文件大小限制和 UI 行为仍由现有 hook/props 驱动。
- **针对性测试**：`web/tests/reviewer-architecture-rules.test.mjs` → 守住组件 service 边移除；`npm run typecheck` 与 file editor 测试守住实际编辑契约。

### G-IS Document preview data loading uses colocated hook

- **可见症状**：DocumentViewer 直接请求受保护文件并在渲染组件里维护取消竞态和错误转换。
- **根因与边界**：请求生命周期迁入 `useDocumentPreviewBlob`；纯错误描述迁入 shared，原组件模块保留兼容导出。加载态、无项目错误、卸载取消和各格式预览行为保持。
- **针对性测试**：`web/tests/document-preview-view-split.test.mjs` → 守住 view/hook/service 边界和取消/错误契约；`web/tests/document-preview.test.mjs` → 守住共享预览工具；架构门禁与 typecheck 守住 import 边。

### G-IT Project and Git views use shared contracts and node logic hook

- **可见症状**：ProjectAddPopover 和 NodeManagerPanel 的展示组件直接依赖节点注册 service；SessionMainContent 通过 service 模块导入 Git diff 类型。
- **根因与边界**：节点注册表读取由 colocated hook 调用，节点 id/调色板和 Git diff 契约归 shared；service 模块保留 Git diff 类型 re-export。节点切换、颜色展示与 payload 字段不变。
- **针对性测试**：`web/tests/project-add-node-boundary.test.mjs` → 守住三个组件的导入边界与 hook 接线；typecheck 守住 shared/service 兼容类型。

### G-IU File and session service work moves behind logic hooks

- **可见症状**：FileEditor、WebPushMenuItem 与 SessionViewer 直接导入文件、推送、会话 service，在展示组件中调用请求/投影逻辑。
- **根因与边界**：文件重载、推送订阅/测试通知状态机、持久 exchange 筛选与合并由 colocated hooks 提供；页面动作顺序、通知文案、取消/失败反馈与窗口合并语义保持。
- **针对性测试**：`web/tests/file-session-service-boundaries.test.mjs` → 守住 view/hook/service 边界；既有 session merge、file editor 与 WebPush 测试及 typecheck 守住行为契约。

### G-IV Vitest-only frontend tests and universal agent rules

- **可见症状**：多个前端测试运行器会造成验证结果不一致；按路径或历史状态放行会让存量代码绕过新代码必须遵守的规则。
- **根因与边界**：`npm test` 仅运行 Vitest。规则入口、reviewer 门禁、TODO、计划状态与机器登记共同描述同一套普遍适用的要求；任务提示不能豁免仓库规则。历史记录可保留当时的测量值，但不得作为当前基线。
- **针对性测试**：`agent-docs-contract.test.mjs` → Vitest 唯一入口、ship 文档与规则/计划入口契约；`naming-convention.test.mjs` → 普遍适用的命名与后缀检查；`reviewer-architecture-rules.test.mjs` → 依赖边与文件大小守卫；`source-map.test.mjs` → 逻辑源码映射。为满足统一文件大小规则，本批从应用入口、任务样式与 locale 表中提取职责模块；`App.tsx` 组合根不变。

### G-IW Frontend naming rules and standalone entry alignment

- **可见症状**：命名文档声称已有“文件名 = 主 export”门禁，但测试没有实现；多个独立组件文件名不能反映主组件，返回导航 hook 与浏览器历史原语混放在 `hooks/`。
- **根因与边界**：AST 门禁只约束独立组件和 hook 入口，职责模块仍可包含多个相关导出；同时保证 `.ts` 无 JSX、`.tsx` 有 JSX。AgentSelector 的纯模型/错误解析迁入 `agentSelectorUtils`；返回层 hook 留在 `hooks/useBackLayer.ts`，浏览器历史和层栈原语归 `services/platform/backNavigation.ts`。无 JSX 的工具、兼容 facade 和 shim 改为 `.ts`。其余经审阅的多导出文件按职责保留，不按导出数量拆分。
- **针对性测试**：`naming-convention.test.mjs` → 独立组件/hook 主符号命名与既有后缀规则；`back-navigation.test.mjs` → 返回栈和视图历史行为；`claude-model-1m.test.mjs` → AgentSelector 模型别名解析；toast/session 组件测试与 source-map 测试 → 文件搬迁后原有契约仍生效；agent docs 测试 → Active 计划和 Markdown 链接一致。

### G-IX 排队卡/会话尾遮挡与 assistant 复制透传

- **可见症状**：排队消息卡的 ✎/删除/立即发送按钮下半被输入条盖住；滚到底时最后一条 user 消息的复制等按钮被悬浮输入条挡住半颗；assistant 消息点复制完全没反应。
- **根因与边界**：排队列表底只留 4px 间隙，28px 按钮下沿掉进输入条上沿；会话内容区底留白写死、未用 composerOverlayInset 派生且缺 scroll-padding-bottom；Timeline 分叉处 Assistant 缺 6 个 copy props，点击同步抛 TypeError 且无反馈。只改三处：排队列表底间隙 4px→8px；会话内容底留白与滚动 scroll-padding 由 composerOverlayInset 派生；Timeline 给 Assistant 补齐 copy props。不动 composer 高、不动按钮尺寸、不改 clipboard 链。
- **针对性测试**：`web/tests/queued-overlay-and-copy.test.mjs` → A1 排队卡底间隙 ≥8px、A2 双断点同间隙、B1 会话底留白与 scroll-padding、C1 Timeline 给 Assistant 透传 6 个 copy props。

# App.tsx 深度拆分复盘与下一阶段计划

> 本文是 task-35 分支的独立经验与执行计划，配套 `docs/plans/frontend-split-refactor.md` 使用。
> 记录本轮审核得到的事实、已经验证的做法，以及下一步继续削减 `App.tsx` 的优先级与停损条件。

## 1. 当前基线

- 分支：`task-35`
- HEAD：`待提交`（阶段 C 进行中）
- `web/src/App.tsx`：**4464 行** ✅（阶段目标 ≤4500 达成）
- 历史变化：约 `9585 → 7593 → 6405 → 5979 → 5713 → 5501 → 5416 → 5294 → 5118 → 4918 → 4768 → 4672 → 4571 → 4464`
- 当前目标：按既定决策分阶段继续削减，先达到约 **4000–5000 行**，再重新评估；不直接追求极限小文件。
- **阶段 C 进度（>1200 行的文件，i18n 排除）**：

  | 文件 | 起点 | 现在 | 状态 |
  |---|---|---|---|
  | `components/file/DefaultListView.tsx` | 1296 | 1031 | ✅ 抽 `Breadcrumbs.tsx` |
  | `components/session/SessionList.tsx` | 2191 | 476 | ✅ 拆成 5 个模块 |
  | `components/agent/AgentSelector.tsx` | 1477 | 1191 | ✅ 抽 `AgentSelectorParts.tsx` |
  | `components/file/FileTree.tsx` | 3770 | 2909 | ⏳ 抽 `AgentConfigPopover.tsx`，`FileTreeInner` 还要再拆 |
  | `App.tsx` | 9585 | 4464 | ⏳ 阶段 A 达标，仍 >1200 |
  | `components/session/SessionViewer.tsx` | 3567 | 2378 | ⏳ 顶部 940 行 + 斜杠命令卡片已拆出，剩 `SessionViewerInner` |
  | `services/session.ts` | 2659 | 2659 | ⛔ 被 4 个 VM 测试锁住 |
  | `app/useRealtimeEvents.ts` | 2019 | 1074 | ✅ 34 个 WS 处理器 → `realtimeWsHandlers.ts` |
- 最近完整门禁：`npm run typecheck` 0 错误；`npm test` 210 tests、209 pass、1 skip、0 fail；`make build-web` 成功；`check-upstream` 通过。数字以现场命令为准。

## 2. 本轮审核使用的工具与结论

### 2.1 工具

- **TypeScript Compiler API**：读取真实 AST，计算顶层语句边界、自由变量、声明位置、类型位置和 TDZ 窗口，是拆分判断的主工具。
- **codebase-memory-mcp CLI**：查询架构、调用关系和模块关系，用作检索辅助；事实仍以源码、diff、编译器和测试为准。
- **git / grep / read / wc**：确认提交边界、测试耦合、源码上下文和行数收益。
- **source-map 读层**：`web/tests/source-map.mjs` 维护逻辑路径到物理文件的映射；`source-map-hook.mjs` 保障源码正则测试；`ts-module-hook.mjs` 处理单文件移动。多文件模块需保留原入口/shim。

### 2.2 当前审核结论

最甜的区域是**看板/任务编排块**，而不是继续硬拆互锁的会话 pending/元数据块。当前约在 App.tsx `556–1177`：

- `applyTaskDetails`、`pruneTaskDetails`
- `loadKanbanTasks`
- `handleMoveKanbanTask`
- worktree 分支加载及相关 effect
- 创建/编辑/附件/保存胶水

理论上可减少约 **450–550 行**，但必须拆成两个职责清晰的单元、两个提交：先任务编排核心，再任务创建/编辑。

第二甜点是文件上下文选择块（约 117 行，预计减少 80–100 行）；随后是多项目回复状态轮询（约 205 行，但验证风险较高）。会话 pending/lock 辅助块约 470 行，当前耦合过重，暂不动。

## 3. 前面拆分遇到的困难与已验证解法

### 3.1 最大困难是 TDZ，不是行数

hook 参数对象在调用处立即求值。判据：

```text
max(所有闭包声明结束位置) < min(所有急切引用位置)
```

急切引用包括 hook 参数、依赖数组、JSX 和 render-time 调用；`useCallback` / `useMemo` / `useEffect` 函数体内引用属于惰性引用。

典型陷阱：`refreshTaskWorktree` 来自 `useProjectTreeWorktrees`，当前在约第 1457 行才初始化；看板块在它之前。新 hook 若在原位置直接传它会触发 TDZ。只能：移动 hook 调用到所有依赖初始化之后、传已初始化的 ref/服务，或重划边界。不能为过类型检查而改变业务执行顺序。

### 3.2 自动抽取的常见误判

- 多行 `useState<T>` 泛型被推成 `any`，造成 setter 回调隐式 any。
- 数组/对象解构和本地别名处理错误。
- JSX intrinsic tag 被误判为自由变量。
- 类型属性名被误判为运行时变量，例如 `{ mindfsView?: MainViewMode }`。
- `catch (err)` 参数被误判成外部自由变量。
- checker 查询独立 AST 节点返回 `any`；必须使用同一个 Program 的 source file。
- 多行 import、传递类型 import 重复，造成 duplicate identifier。
- effect 前契约注释被留在旧文件或完全丢失，破坏源码正则切片。

自动工具只能生成候选补丁；每次抽取后必须检查真实类型、值/类型/transitive imports、调用位置、注释、effect 体与依赖数组。

### 3.3 源码正则测试与运行时测试是两种约束

source-map 只解决源码读取，不是生产模块解析器，也不改变 VM 中的 `require`，更不能证明运行时行为未变。`session.ts` 的 4 个 VM 测试使用 `ts.transpileModule + vm.runInContext`，sandbox `require` 不支持拆后的相对导入；必须先改造 sandbox require 并喂入 MODULES，未解锁前不要拆 `session.ts`。

### 3.4 运行时覆盖不足

历史测试主要是源码契约和类型检查。已有 Playwright/pytest 冒烟夹具应作为独立验证：隔离 XDG 配置、临时账号和随机端口，验证 App-ready、项目、会话、消息输入等标记；不纳入常规 `npm test`，也不通过重启服务规避问题。

## 4. 工具选择：应该使用什么、避免什么

### 应该使用

- TypeScript Compiler API：AST、声明顺序、真实类型和语句边界。
- codebase-memory-mcp CLI：调用图和模块关系初筛。
- source-map 与两个 preload hook：保持源码契约测试。
- 小步 git 提交：每次一个职责簇，提交信息带 `Scope: G-BA`。
- 定向测试矩阵，再跑全量 typecheck/test/build/check-upstream；需要时独立 e2e。

### 明确避免

- **不使用 ast-grep、tree-sitter**：本任务已明确禁用，不重新引入。
- 不把 `/tmp` 脚本当作不可复现依赖；窗口分析应使用可复现只读脚本或记录判据。
- 不用正则替代 AST 做边界、作用域和类型推断。
- 不逐个修改 40+ 源码正则测试来适配拆分；先登记 source-map。
- 不把 source-map 当运行时解析器或 VM require 兼容层。
- 不把 e2e 混入常规 npm 门禁。
- 不在一个提交里混合 App 拆分、行为修复、session.ts VM 解锁和其他大文件重构。
- 不用 `--theirs` / `--ours` 粗暴解决 main 冲突。
- 不顺便改 wire 格式、状态键、localStorage 键或业务逻辑。

## 5. 下一阶段计划：先砍最甜的任务编排

### T1：任务编排核心（第一刀）✅ 已完成

范围以 AST 语句边界为准，目标文件 `web/src/app/useTaskBoard.ts`：

- `applyTaskDetails`
- `pruneTaskDetails`
- `refreshWorkspaceBoard`
- `loadKanbanTasks`
- `handleMoveKanbanTask`
- 直接绑定的任务状态更新/重拉逻辑

职责是任务详情缓存合并与淘汰、看板加载、Abort/seq 防旧请求覆盖、任务移动/删除/worktree rebuild/finish，以及必要的工作台结构刷新。

暂不纳入任务创建编辑、附件上传、通用偏好 effect、session pending/lock，以及必须改变 `useProjectTreeWorktrees` 初始化顺序的逻辑。

**TDZ**：`applyTaskDetails → loadKanbanTasks → handleMoveKanbanTask` 是安全依赖链，但 `refreshTaskWorktree` 后置初始化。第一刀应把它排除为可选回调，或将 hook 调用点后移；先用 AST 计算所有急切依赖声明位置。预计 App 减少约 300–350 行。

定向测试：`task-cache-eviction`、`task-stage-panel`、`task-panel-unification`、`workspace-board`、`task-button-gates`、`task-template-node-routing`、`project-tree-refresh`、`upstream-restore`。

### T2：任务创建/编辑编排（第二刀）✅ 已完成

目标文件 `web/src/app/useTaskComposer.ts`，范围约为：

- `openTaskCreateDialog`
- `switchTaskInlineEditProject`
- `closeTaskEditDialog`
- candidate/attachment 操作
- `saveTaskInlineEdit`
- worktree 偏好与分支加载 effect（仅在窗口成立时纳入）

职责是创建/编辑状态初始化与清理、项目切换时偏好恢复、candidate/attachment/upload progress、createTask payload 组装（含 stages/templateName 快照）。

关键依赖是 `taskTemplates`、`taskStagesForCreate`、`uploadFiles`、`refreshTaskWorktree`、`closeTaskEditDialog` 和 `applyTaskDetails`。优先把调用点放到 `useProjectTreeWorktrees` 之后；若会跨越其他急切 hook，先抽不依赖 worktree 的纯 composer，再接入 worktree 回调。预计再减 180–220 行。

定向测试：`task-stage-panel`、`task-panel-unification`、`task-related-files`、`task-session-jump-root`、`task-template-node-routing`、`workspace-board`、`agent-lifecycle-restart`。

### F：文件上下文选择（第三刀）✅ 已完成

把约 117 行 `buildAttachedFileContext` 与相关 callbacks/effects 抽到 `web/src/app/useAttachedFileContext.ts`。依赖主要是 file/gitDiff/viewer refs 与 setters，预计减少 80–100 行。定向测试：`file-navigation`、`file-editing`、`git-diff-viewer`、`related-files`、`project-tree-refresh`。

### R：多项目回复状态轮询（第四刀）✅ 已完成

约 140 行抽到 `web/src/app/useReplyingSessionPoll.ts`（208 行），保持：每节点显式传 `nodeId`；请求失败不清空其他节点旧状态；只增量重算成功节点；使用 `scopeSessionKey(nodeId, ...)`，不退回 `rootSessionKey()`；不混用 `controlPath` / `appPath`。G-AY 锚点同步迁到新文件。

定向测试：`cross-node-replying-state`、`workspace-node-fanout`、`workspace-realtime-freshness`、`multi-account-partition`、`node-url-normalize`、`pins-authority`。

### 暂缓

A7 会话元数据、A2b pending/queue、`session.ts`、`useRealtimeEvents.ts`。它们要么互锁过重，要么需要先解锁 VM 测试，收益/风险比不如 T1。

## 6. 每一刀固定操作顺序

1. `git status`/`git log` 确认干净。
2. 用 TypeScript AST 计算语句边界、自由变量、声明位置、急切引用。
3. 用 codebase-memory 查询调用/测试/模块关系，仅作辅助。
4. 先确定 hook 调用位置，再生成参数接口；禁止先搬代码后补 TDZ。
5. 机械搬移，保留 effect/callback 体、依赖数组、注释和类型语义。
6. 更新 `web/tests/source-map.mjs`；多文件模块保留旧入口/shim。
7. 更新 G-BA `files`/`tests`/`anchors`，YAML 与 MD 同步。
8. 先 typecheck 和定向测试；失败优先回滚当前簇。
9. 全量 `npm test`、`make build-web`、`check-upstream`；涉及产物时独立 e2e。
10. 一个职责簇一个提交，提交信息包含 `Scope: G-BA`，记录行数和验证结果。

## 7. 停损与验收标准

遇到以下任一情况，停止当前刀并重新划边界：需要改业务逻辑/依赖数组/调用顺序/状态清理顺序；出现无法解释的 `any`、重复 import、类型泄漏；定向测试失败且不是纯路径映射；source-map 需要改变测试语义；需要同时修改 session.ts、VM sandbox 或后端；参数超过可读边界且无明确领域对象；TDZ 只能靠提前执行或移动状态初始化规避。

验收不只是行数减少，还包括：新 hook 职责单一且可定位；typecheck、全量测试、构建和门禁全绿；节点、账户、pending、任务等高风险测试无回归；逻辑旧路径仍可由 source-map 读取。

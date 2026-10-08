# 深度拆分第二阶段计划（2026-10-07）

## 一、当前基线

- `App.tsx`：**4182 行**（本轮起点 4464），Phase 1 已完成五刀。
- 新增 `src/app/useTaskWorkspaceState.ts`：132 行，集中管理任务/看板 state、refs、上传与 worktree 状态。
- 新增 `src/app/useTaskWorkspaceEffects.ts`：45 行，承接 bootstrap ready 后的任务模板、Kanban、跨项目会话刷新 effect。
- 新增 `src/app/useRootContentView.ts`：192 行，根内容视图模型（工作树归属、关联文件、仓库/HEAD 分组、git 统计刷新键）。
- 新增 `src/app/useProjectTreeActions.ts`：188 行，项目树四个 tab 的强制重拉 + 管理节点刷新 + worktree 自动展开。
- 新增 `src/app/useUiPreferences.ts`：102 行，纯 UI 偏好状态与持久化 effect。
- 非 i18n 最大文件：`AgentSelector.tsx` 1191、`FileTree.tsx` 1153、`services/session.ts` 1138、`SessionViewer.tsx` 1127。
- 当前工作区基于 `task-35`，主检出存在独立 WIP；本阶段只在当前 worktree 修改，不触碰主检出。
- 已有门禁基线：Web 210 tests（209 pass / 1 skip）、Go tests 全绿、e2e 10 passed、build-web 成功。

## 二、本阶段目标

### 主目标

将 `App.tsx` 从 **4464 行降至约 3000 行**，优先把状态编排与领域副作用移到可测试的领域 hook，保持现有行为不变。

### 硬约束

1. 非 i18n 文件继续保持 `<1200` 行；
2. 不修改主检出 `/home/xiaokubao/projects/mindfs` 的 WIP；
3. 不把 e2e 接入常规 `npm test`；
4. 每一刀都必须保留现有 source-map / VM / 行为测试语义；
5. 若需要改变依赖数组、effect 顺序、状态清理顺序或业务逻辑，立即停止该刀并重划边界。

## 三、现状分析

`App.tsx` 的结构已不是巨型 JSX，也不是「几块大石头」，而是**高度碎片化**：
App() 函数体共 **398 条顶层语句**，除 JSX 外最大单块只有 105 行。

用 TypeScript AST 实测（`/tmp/mfsprobe/glue.mjs`，不落在仓库里）拆分这 4362 行：

| 组成 | 行数 | 性质 |
|---|---:|---|
| JSX return | 324 | 1 条语句，已大量委托给子组件 |
| **hook-call glue** | **1215** | 68 个已抽出 hook 的 props 对象，**已委托，无肉可切** |
| useEffect | 481 | 58 条，最大 35 行、多数 7–16 行 |
| state / ref / useMemo / useCallback | 1811 | 领域状态与其派生值 |
| 其它可搬逻辑 | 531 | 少量 handler 与工具函数 |

**结论：剩余的「肉」几乎全在 1811 行的 state/派生 与 481 行的 effect 里**，而且必须
连同各自的 handler 一起整域搬走；单条语句切出去没有收益。

**3000 行的可达性**：1215（glue）+ 324（JSX）= 1539 行基本固定。要从 4182 降到 3000，
需要再搬走约 1180 行，即 1811+481+531 中的约 42% —— 约 **8–12 次领域抽取**。
所以 3000 是可达的，但不是「几刀」的事，需要按领域持续搬。

最容易切的领域（自包含、依赖少）：UI 偏好、任务/看板、根内容视图、项目树动作 —— 本轮已完成。
剩余领域按风险从低到高：会话列表/归档、节点/项目加载、文件树、插件、e2ee/更新、session pending/realtime。

不建议先碰 session pending / restore / realtime：这些区域共享 ref 多、effect 顺序敏感、回归半径大。

## 四、执行顺序

### Phase 1 当前进度（2026-10-07）

- ✅ `useTaskWorkspaceState.ts`：任务/看板 state、refs、上传状态、worktree 状态 + 两个同步 ref effect。
- ✅ `useTaskWorkspaceEffects.ts`：bootstrap ready 后的任务模板 / Kanban / multi-project session 初始化刷新。
- ✅ `useRootContentView.ts`：工作树归属、会话关联文件、仓库/HEAD 分组、git 统计刷新键（纯派生）。
- ✅ `useProjectTreeActions.ts`：项目树四 tab 强制重拉 + 管理节点刷新 + worktree 自动展开。
- ✅ `useUiPreferences.ts`：5 个 UI 偏好 + 6 个持久化 effect + slash 复制定时器清理。
- `App.tsx`：4464 → 4411 → 4404 → 4309 → 4224 → **4182 行**，本轮累计减少 **282 行**。
- 下一刀：会话列表 / 归档领域（`useSessionArchive` 已在，可继续收口 archive panel 的 state 与 effect），
  或节点/项目加载领域的 `selectRootNode` 周边（77 行，是剩余最大的单块）。


1. **任务状态容器 `useTaskWorkspaceState.ts`**
   - 承接任务相关 `useState` / `useRef`：
     `kanbanTasks`、`kanbanTaskCountItems`、`taskDetailsById`、`taskFirstInputById`、
     `taskSessionKeysById`、`taskRelatedFilesById`、`selectedKanbanTaskId`、
     `taskInlineEdit`、候选项、上传进度、worktree branch 状态、collapse 状态。
   - 返回显式 state/setter/ref 对象，不隐藏现有状态更新时序。
   - 第一刀只搬声明和同步 ref effect，不搬业务 effect。

2. **任务副作用 hook `useTaskWorkspaceEffects.ts`**
   - 承接任务模板懒加载、Kanban reload、worktree branch 加载、任务详情刷新相关 effect。
   - 依赖数组逐一照搬；不把多个 effect 合并成一个 effect。
   - 保留 `AbortController`、load sequence 与 stale response 防护。

3. **任务选择 / 面板派生 hook `useTaskWorkspaceSelection.ts`**
   - 承接 `selectedKanbanTask`、`selectedKanbanTaskSessionKey`、任务详情 fallback、task stage 派生。
   - 只移动 `useMemo` 和纯派生 callback，不改变选中任务语义。

### Phase 2：节点 / 项目加载领域

目标：再减少约 500–700 行。

1. **`useManagedRootState.ts`**
   - 承接 managed root / node refs、`currentRootId`、`currentRootNodeId`、树加载序列、节点失败集合。
2. **`useProjectNavigationEffects.ts`**
   - 承接 root/node 切换时的清理、abort、跨节点隔离、文件树与 Git 刷新 effect。
3. **`useNodeRouting.ts`**
   - 承接 `getNodeIdForRoot`、`scopedRootKey`、复合 session/root key 解析。
   - 只抽纯函数与稳定 callback，避免把路由状态再复制一份。

### Phase 3：会话列表领域（最后处理）

目标：减少约 400–600 行。

1. **`useSessionListState.ts`**
   - 承接 sessions、multi-project groups、archive panel、loading / pagination、list mode、pins overlay。
2. **`useSessionListEffects.ts`**
   - 承接列表拉取、归档懒加载、multi-node pending 对账、debounce / abort / stale response。
3. **`useSessionSelectionState.ts`**
   - 承接 selected session / drawer / bound session / interaction mode 的状态编排。

## 五、每刀的安全流程

1. 用 TypeScript AST 获取完整 declaration / effect 范围，不按手写行号切嵌套 JSX。
2. 先写新 hook，props 使用领域 context 对象，不制造 30+ 个散 props。
3. 替换 App.tsx 中的声明与调用点。
4. 运行 `npm run typecheck`。
5. 运行定向测试（若涉及 source-map / session 则先跑对应 VM 测试）。
6. 运行 `npm test`。
7. `wc -l` 核对 App.tsx 和新模块，确认新模块 `<1200`。
8. 单独提交，commit message 以 `Scope: G-BA` 结尾。
9. 每个阶段完成后刷新本计划文档的真实行数。

## 六、停损条件

遇到以下任一情况，不继续硬切：

- 需要修改业务逻辑、依赖数组、effect 顺序或状态清理顺序；
- 出现无法解释的 `any`、隐式类型泄漏、TDZ 或循环初始化；
- 定向测试失败不是单纯 source-map 路径问题；
- 新 hook 需要接收超过约 20 个没有领域意义的 props；
- 为通过测试而削弱已有测试语义；
- 新模块超过 1200 行；
- 只能靠 `git restore` 后再次手工试错，无法给出可靠 AST 边界。

## 七、阶段验收目标

| 阶段 | App.tsx 目标 | 验收 |
|---|---:|---|
| 当前基线 | 4464 | 已完成 |
| Phase 1 任务领域 | ≤3700 | typecheck + npm test + 行数 |
| Phase 2 节点项目 | ≤3200 | typecheck + npm test + e2e |
| Phase 3 会话列表 | **约3000** | 全量门禁 + 文档刷新 |

如果 Phase 1 后 App.tsx 已明显低于 3700，但 hook props 开始膨胀，则停止在该阶段，不追求数字而破坏边界。

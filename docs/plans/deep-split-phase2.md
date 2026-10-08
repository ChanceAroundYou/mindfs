# 深度拆分第二阶段计划（2026-10-07）

## 一、当前基线

- `App.tsx`：**4411 行**，Phase 1 已完成第一刀（任务工作区状态容器）。
- 新增 `src/app/useTaskWorkspaceState.ts`：132 行，集中管理任务/看板 state、refs、上传与 worktree 状态。
- 非 i18n 最大文件：`AgentSelector.tsx` 1191、`FileTree.tsx` 1153、`SessionViewer.tsx` 1127、`services/session.ts` 1138。
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

`App.tsx` 的结构已不是巨型 JSX：

```text
1..254      imports / types
255..4141   App() hook 体：约 3887 行，61 个 useEffect、72 个 useCallback
4142..4464  JSX：约 323 行
```

因此下一阶段不再从 JSX 下手，而是按**状态所有权 + 副作用边界**抽 hook。

最适合的区域是看板/任务编排，因为 App.tsx 已经把 `useTaskTemplates`、`useTaskBoard`、`useTaskComposer`、
`useTaskInlineCandidates` 接好，存在天然的领域上下文；不建议先碰 session pending / restore / realtime，
这些区域共享 ref 多、effect 顺序敏感、回归半径大。

## 四、执行顺序

### Phase 1 当前进度（2026-10-07）

- ✅ `useTaskWorkspaceState.ts`：抽出任务/看板 state、refs、上传状态、worktree 状态与两个同步 ref effect。
- `App.tsx`：4464 → **4411 行**，首刀减少 53 行；状态所有权已经从 App 组件体集中到任务领域 hook。
- 下一刀：`useTaskWorkspaceEffects.ts`，只迁移任务模板 / Kanban / worktree 的 effect，严格保持依赖数组与 abort / sequence 防护。


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

# 子会话树状态机收束 — 详细执行方案

## 实施结果（2026-10-18）

- 新增 `useSessionChildTree.ts`，唯一管理子树展开、加载、收起、折叠基数和 row 派生。
- `SessionList.tsx` 与 `useMultiProjectSessionList.ts` 已接入；多项目继续用 `nodeId + rootId + sessionKey` 作用域 key，并保留其 localStorage 持久化。
- 两个列表的渲染层只调用 `toggleChild` / `collapseChild`，不再直接写子树展开 state。
- `archived-panel.test.mjs` 已从“双份实现”断言改为共享 hook 的不变量和接线断言。
- 已验证 `make test`、`web npx tsc --noEmit`、`web npm test` 通过；`check-upstream` 待提交后运行（其 delta 只统计已提交文件）。

## 原方案记录

把单项目 `SessionList.tsx` 与多项目 `useMultiProjectSessionList.ts` 中重复的子会话树状态机收敛到一个共享 hook，消除双份实现，同时保持 key 作用域、加载、收起和基数不变量。

不改变：
- 项目分组展开/收起；
- 子会话默认 3 条、选中链路自动可见、分页、收起；
- 搜索、归档、外部导入；
- localStorage / state key 语义。

## 现状定位

### 单项目：`web/src/components/session/SessionList.tsx`

| 行 | 内容 |
|---|---|
| 60-65 | `expandedChildren`、`loadingChildren`、`childrenHasMore`、`collapsedBaseCount` 四个 state |
| 67-72 | `pruneChildState` effect，按当前 `sessions` 清理失效 state |
| 73-138 | `visibleSessions` useMemo，内含建树、fork 顶级、选中父链、3 条上限、基数计算、toggle row 生成 |
| 165-176 | `loadChildren(parent, beforeTime)` |
| 179-200 | `handleChildToggle(row)` |
| 411-427 | 子会话 toggle 渲染，直接 `setExpandedChildren` 收起 |

### 多项目：`web/src/components/session/useMultiProjectSessionList.ts`

| 行 | 内容 |
|---|---|
| 78-83 | 四个 state |
| 102-112 | `pruneChildState` effect |
| 179-196 | `loadChildren(parent, beforeTime, fallbackRootId, fallbackNodeId)` |
| 198-262 | `buildRows(sessions, fallbackRootId, fallbackNodeId)` |
| 264-289 | `handleChildToggle(row, groupSessions, fallbackRootId, fallbackNodeId)` |
| 220-232 | 选中父链计算 |
| 233-261 | 建树、append、3 条上限、基数、toggle row |

### 多项目渲染：`MultiProjectSessionList.tsx`

| 行 | 内容 |
|---|---|
| 193-222 | 子会话 toggle 渲染，直接 `setExpandedChildren` 收起 |
| 133 | `expanded` 派生，仅项目分组 |
| 137 | `sessions = expanded ? group.sessions : []` |
| 138 | `rows = buildRows(...)` |

## 新增文件

### `web/src/components/session/useSessionChildTree.ts`

职责：管理子会话树状态与派生，不管理项目分组。

```ts
type Scope = { rootId: string; nodeId: string };
type ChildTreeOptions = {
  sessions: SessionItem[];
  selectedKey: string;
  scopeMatches: (rootId: string, nodeId: string) => boolean;
  stateKey: (session: SessionItem, rootId: string, nodeId: string) => string;
  onLoadChildren?: (parent, beforeTime?, scope?) => Promise<{ hasMore?: boolean } | void>;
};
```

内部 state：
- `expandedChildren`
- `loadingChildren`
- `childrenHasMore`
- `collapsedBaseCount`

内部 effect：
- 监听 `sessions`，按所有 `session.key` prune 前三个 state。

输出：
- `buildRows(sessions, rootId, nodeId): VisibleSessionRow[]`
- `toggleChild(row, sessions, rootId, nodeId)`
- `collapseChild(row, rootId, nodeId)`
- `loadChildren(parent, beforeTime, rootId, nodeId)`
- `loadingChildren`, `childrenHasMore`, `expandedChildren`

不变量保持：
- fork 会话仍为顶级；
- 非选中父会话不显示子项；
- 选中子会话的父链仍可见；
- 折叠态最多 3 条；
- `collapsedBaseCount` 在展开前钉住；
- 有下一页时继续拉，否则收起。

## 修改计划

### 1. `SessionList.tsx`

删除：
- 60-65 四个 state
- 67-72 prune effect
- 73-138 中的建树、父链、3 条上限、基数、toggle row 逻辑
- 165-176 `loadChildren`
- 179-200 `handleChildToggle`

改为：
- 调用 `useSessionChildTree`，传入 `sessions`、`selectedKey`、`scopeMatches: () => true`、`stateKey: (session) => session.key`、`onLoadChildren`。
- `visibleSessions = childTree.buildRows(sessions, "", "")`，搜索模式仍直接映射。
- `handleChildToggle = (row) => childTree.toggleChild(row, sessions, "", "")`
- 渲染中 `loadingChildren`、`childrenHasMore`、`expandedChildren` 改为从 hook 读取。
- 收起按钮改为 `childTree.collapseChild(row, "", "")`。

### 2. `useMultiProjectSessionList.ts`

删除：
- 78-83 四个 state
- 102-112 prune effect
- 179-196 `loadChildren`
- 198-262 `buildRows`
- 264-289 `handleChildToggle`

改为：
- 调用 `useSessionChildTree`，传入：
  - `sessions`：所有 groups 的扁平会话可合并，或由 hook 支持 scope；
  - 更稳妥：hook 支持 `buildRows(sessions, rootId, nodeId)` 与 `toggleChild(row, sessions, rootId, nodeId)`，state key 由 `stateKey` 生成；
  - `scopeMatches: (rootId, nodeId) => rootId === selectedRootId && nodeId === selectedNodeId`
  - `stateKey: (session, rootId, nodeId) => childStateKey(session, rootId, nodeId)`
  - `onLoadChildren: (parent, beforeTime, rootId, nodeId) => onLoadChildren?.(parent, beforeTime ? { beforeTime } : undefined)`，hook 内部再套 root/node。
- `buildRows`、`handleChildToggle`、`childStateKey`、`loadingChildren`、`childrenHasMore`、`expandedChildren` 从 hook 暴露。
- `setExpandedChildren` 直接暴露 hook 的 setter，供 `MultiProjectSessionList` 收起按钮使用。

### 3. `MultiProjectSessionList.tsx`

- `expandedChildren`、`setExpandedChildren`、`loadingChildren`、`childrenHasMore`、`childStateKey`、`buildRows`、`handleChildToggle` 继续从 hook 解构。
- 渲染逻辑不变，只移除对 `useMultiProjectSessionList` 中重复子树 state 的依赖。

### 4. 测试

新增 `web/tests/session-child-tree.test.mjs`，覆盖：
- fork 会话保持顶级；
- 非选中父会话不显示子项；
- 选中子会话的祖先链可见；
- 折叠态最多显示 3 条；
- 展开前基数在加载更多后仍保持；
- 不同 scope key 的同名会话互不串态。

修改 `web/tests/archived-panel.test.mjs`：
- `SessionList.tsx` 中直接 state 的断言改为检查 `useSessionChildTree` 接线，或把断言迁移到新 hook 文件；
- `showExpandIcon` 断言改为检查共享 hook 与两个列表的接线，避免文件拆分后误报。

## 验收

- `cd web && npx tsc --noEmit`
- `cd web && npm test`
- `make check-upstream`
- 手动验证单项目与多项目子会话展开、加载更多、收起、选中链路、跨节点 key 隔离。

## 实施顺序

1. 新增 `useSessionChildTree.ts`，先用单项目接入，跑测试。
2. 多项目接入，跑测试。
3. 更新 archived-panel 断言与新增 hook 测试。
4. 门禁与 ship。

# 项目身份跨节点唯一：同名 / 同路径项目不再互相顶替

2026-11-20，用户直接要求：「不同节点的同名项目、同路径项目应该被识别为不同的项目，不应该出错」。

承接 G-NP（工作台卡片动作按卡片自己的节点派发）。G-NP 只修了卡片动作一处，
这一类问题的根还在身份层：**裸 `rootId` 被当成项目身份**。`root_id` 是一台机器内部的
项目 id（常常就是目录名或绝对路径），不同机器各有一份互不相干的同名项目是常态
（实测 fn 与 pc 都有 `docs`、都有 `CMAIA`），于是任何「按 rootId 反查节点」的写法
都只能命中其中一台。

## 身份契约

**项目身份 = `(nodeId, rootId)`**，即 `scopeKey(nodeId, rootId)`。

裸 `rootId` 只在两种情况下足够：

1. 它就是**当前选中的项目**（`currentRootId` + `currentRootNodeId`）；
2. **全表只有一台机器**拥有这个 id。

其余情况必须由调用方带显式 `nodeId`；解析不出来时返回 `undefined`（交给 active node
兜底并暴露问题），**不再「首个获胜」**——`indexManagedRoots` 的 `byId` 只保留第一个，
正是 G-NP 之前所有错派的源头。

## 改动

| # | 位置 | 改什么 |
|---|---|---|
| 1 | `web/src/shared/projectIdentity.ts`（新） | `resolveProjectNodeId` 纯函数：显式节点 → 当前选择 → 唯一归属 → undefined |
| 2 | `web/src/app/appMisc.ts` | `indexManagedRoots` 两遍扫描：`byId` 只放唯一归属（重复 id 不猜），新增 `nodesById` |
| 3 | `web/src/services/net/rootNode.ts` | 注册表 `rootId → nodeIds` + 选择覆盖；`getRootNodeId(rootId, preferredNodeId?)` |
| 4 | `web/src/app/useAppRootScopeState.ts` | `getNodeIdForRoot` 走同一套规则，新增 `getNodeIdForProject(rootId, nodeId?)` |
| 5 | `web/src/app/useAppProjectDisplay.ts` | `getRootDisplayName` / `getDisplayNodeColor` 接受 nodeId，按 `byKey` 解析 |
| 6 | `web/src/app/useWorkspaceBoard.ts` | 分组名/色取**分组自己的节点**；同一 rootId 出现在多台机器上时组名追加节点名 |
| 7 | `web/src/app/useTaskBoard.ts` | `pruneTaskDetails(rootId, keep, nodeId?)` 按 `(node, root)` 圈定，不再跨节点清同名项目 |
| 8 | `web/src/app/openDirectoryAction.ts` | `/api/tree` 用**已解析出的节点**，不再回头按 root 反查 |
| 9 | `web/src/components/app/TaskDetailPanelGate.tsx` | 详情/摘卡用任务自带的 `_nodeId` |
| 10 | `web/src/plugins/manager.ts` + `plugins/trust.ts` | 插件表与信任记录按 `scopeKey(node, root)` 分桶，旧裸键只读兼容 |

## 验收

- 新增 `web/tests/project-identity.test.mjs`：`resolveProjectNodeId` 行为 + 源码守卫（byId 两遍扫描、
  rootNode 唯一归属、工作台按 group.nodeId 取名、prune 带 nodeId、tree 用 resolvedNodeId）。
- 关键守卫做变异验证（改回首个获胜 / 去掉 node 参数 / 退回 root 反查），各自打红后还原。
- `tsc -b --noEmit`、`npx vitest run`、`make check-upstream`。

## 边界

- 服务层仍有大量 `getRootNodeId(rootId)` 单参调用，它们只服务**当前视图**：选择覆盖
  （`setRootNodeId`）保证解析到用户点开的那台机器。跨节点列表里的条目必须自己带 `nodeId`。
- `taskDetailsById` 仍以裸 task id 为键：`newID` 是 64 位随机，跨节点碰撞概率可忽略；
  本轮只修 prune 的作用域。
- 后端不改：每个节点的库只认自己的 root，碰撞只发生在前端聚合层。

## 执行结果（2026-11-20，Scope: G-NR）

全部按上表落地，另补了表里没写、但不补就会漏的三处：

- `useManagedRoots.refreshManagedRoots`：判「项目还在不在」从 `rootID in nextRootById` 改为
  `nextRootIdSet.has(rootID)`（id 全集）。否则同名项目在 byId 里查不到，每次刷新都被当成
  已删除，清空选中/绑定/缓存。
- `useManagedRoots` 的 byKey 重建改成**全量替换 + 保住本轮拉取失败的节点**（原来是
  `{...旧, ...新}` 合并，删掉的项目永远留在表里）。
- `useAppProjectRemoval`：删项目/按路径找项目改走完整索引 `byKey`（带当前选中节点），
  byId 里没有同名项目那一份，按老写法会报「找不到项目路径」。
- `useTaskBoard` 的 prune 逻辑抽成 `useTaskDetailsPrune.ts`（useTaskBoard 加完节点判定后
  509 行，超 500 硬线），`source-map-modules-app.mjs` 的 `src/App.tsx` 逻辑条目同步登记。

顺带对齐的四处既有守卫（都按新契约改写、语义更强，没有放宽）：

- `conversation-view.test.mjs`：详情面板 remount 键从 `root_id::taskId` 改为
  `_nodeId::root_id::taskId`。
- `task-cache-eviction.test.mjs`：`inScope` 断言改为 `if (task.root_id !== rootId) return false;`
  （原来是单行箭头函数写法）。
- `task-ghost-card.test.mjs`：`pruneTaskDetails(rootId, keep, nodeId)`。
- `workspace-realtime-freshness.test.mjs`：`nodeId={taskNodeId(...)}`。

**验证**：`web/tests/project-identity.test.mjs` 14 条通过（`resolveProjectNodeId` /
`rootNode` 模块行为 + 9 条源码守卫）。五处变异各自打红后还原：byId 首个获胜、多归属
也猜第一个、存在性按 byId 判、裁剪不看节点、`/api/tree` 退回 rootId 反查。
`npx tsc -b --noEmit` clean；`npx vitest run` 252 文件 / 791 通过 / 6 跳过；
`make check-upstream` 见提交信息。

**未验证 / 不覆盖**：真机双节点联调（fn 与 pc 的 `docs` 同时打开）未做；服务层单参
`getRootNodeId` 调用点未逐个改；左侧文件树顶层仍按 root id 去重（两台的 docs 共用一行，
展开按节点分桶）。

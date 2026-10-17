# 项目分组自动展开/折叠（随节点切换）— 移除方案

## 范围（仅此）

只移除「切换选中节点时，当前节点的项目分组默认展开、其他节点分组默认折叠」这一自动机制。**手动点击项目名收起/展开（handleProjectHeaderToggle）保留；底部「还有 N 个 / 收起」加载更多保留；子会话折叠/展开保留；pinned 排序保留。**

## 现状机制

| 机制 | 位置 | 作用 |
|---|---|---|
| `groupIsCurrentNode` | `useMultiProjectSessionList.ts:294-298` | `group._nodeId === selectedNodeId` |
| `groupDefaultExpanded` | `:300` | 未显式记录时默认值 = `groupIsCurrentNode(group)` |
| 派生 `expanded` | `MultiProjectSessionList.tsx:133` / hook :304,:337 | `expandedProjects[key] ?? groupDefaultExpanded(group)` |

后果：切换节点时，新节点分组默认展开、旧节点分组默认折叠（除非 localStorage 有显式记录）。

## 改动

### `useMultiProjectSessionList.ts`

- `groupDefaultExpanded` 改为恒返回 `true`（所有分组默认展开，与节点无关）。
- 删除 `groupIsCurrentNode`（无其他引用）。
- `expandedProjects` state、localStorage、persist effect、`handleProjectHeaderToggle` 全部保留（手动交互不受影响）。

### 不动

- `expandedProjects` / `handleProjectHeaderToggle` / `handleProjectToggle` / `handleProjectCollapse`。
- `MultiProjectSessionList.tsx` 的 `expanded` 派生与头点击 `onClick`。
- 子会话折叠、底部加载更多、pinned 排序。

## 验收

- 切换节点后，各节点分组的展开态不再因节点切换而变化；手动收起的分组保持收起。
- `tsc --noEmit` / `npm test` 全绿。
- 登记 `docs/upstream-customizations.yaml`，`make check-upstream` 通过。

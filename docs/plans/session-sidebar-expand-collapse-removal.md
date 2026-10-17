# 右侧会话栏折叠/展开收束 — 执行方案

## 实施结果（2026-10-18）

- `c9890788`（G-JH）已删除节点切换驱动的项目分组自动展开/折叠；项目默认展开，手动项目头状态仍按节点+项目持久化。
- 本次已删除项目底部 toggle 的“收起”职责：底部仅在仍有未加载顶级会话时显示，且只触发加载更多；项目名称是唯一的项目级展开/收起入口。
- 子会话树状态机的共享提取已移入独立计划 `session-child-tree-consolidation.md` 并实施。

## 原方案

## 目标

把右侧会话栏的折叠/展开职责收束为三条明确规则：

1. **项目分组**只由项目名称点击收起/展开；该状态按 `nodeId + rootId` 持久化。
2. **项目底部行**只负责继续加载未加载的顶级会话，永远不再承担“收起”。
3. **子会话树**继续保留“选中链路可见 + 前 3 条 + 加载更多/收起”，但单项目和多项目列表共用同一套状态、树构建与加载逻辑。

搜索结果、归档面板、外部会话导入分页是独立视图或数据分页，不是树/项目的折叠机制，本计划不改。

## 已完成：删除节点切换自动改变项目展开态

已在 `c9890788`（G-JH）完成：

- 删除 `groupIsCurrentNode`。
- 未记录过手动状态的项目分组一律默认展开，不再依赖 `selectedNodeId`。
- 保留 `expandedProjects`、localStorage 和项目名称点击；用户手动折叠的状态不受切换节点影响。

## 现状与问题

### A. 项目分组有两个“收起”入口

| 入口 | 位置 | 当前职责 |
|---|---|---|
| 项目名称 | `MultiProjectSessionList.tsx` → `handleProjectHeaderToggle` | 用户主动展开/收起项目；展开时补拉未加载顶级会话 |
| 项目底部行 | `ToggleRowButton` → `handleProjectToggle` / `handleProjectCollapse` | 同时做“还有 N 个”的加载更多和“收起” |

底部行的文字会在“还有 N 个”和“收起”间切换，且 `ToggleRowButton` 对展开态优先走 `onCollapse`。它和项目名称是重复的收起入口，混合了分页与可见性两个职责。

### B. 子会话树逻辑复制了两次

`SessionList.tsx` 和 `useMultiProjectSessionList.ts` 都实现了：

- 根据 `parent_session_key` 建树（fork 会话保持顶级）；
- 计算选中会话的父链；
- 默认只显示 `COLLAPSED_CHILD_SESSION_LIMIT`（3）个子会话；
- 记录展开前基数，避免“展开后再收起，剩余数从 2 变 102”；
- 加载下一页子会话、收起、以及删除/归档后清理无效状态。

两份实现目前行为相同，但日后修一侧极易漏另一侧；跨节点列表还额外承担 state-key 作用域，不能靠复制维持一致。

## 计划改动

### 1. 项目底部行拆回纯“加载更多”

**修改：** `useMultiProjectSessionList.ts`、`MultiProjectSessionList.tsx`。

- 删除 `handleProjectToggle` 与 `handleProjectCollapse`。
- 新建语义单一的 `loadMoreProject(group)`：仅在仍有未加载顶级会话时调用 `onLoadMoreProject`，并维护 `loadingProjects`。
- 项目底部行仅在 `remaining > 0` 时渲染，文案始终是“还有 N 个”，点击始终加载下一批；不传 `onCollapse`，不显示收起箭头。
- 项目名称的 `handleProjectHeaderToggle` 保留：
  - 收起：只更新 `expandedProjects`；
  - 展开：恢复已加载会话，并在存在未加载顶级会话时继续按当前行为请求补页。
- `expandedProjects`、`mindfs:sessionPanelExpandedProjects:v1`、节点+项目分片 key 不变。

**结果：** “项目折叠”只有项目名称一个入口；底部只表达数据分页，不再存在文本和实际动作不一致。

### 2. 提取共享的子会话展开 hook

**新增：** `web/src/components/session/useSessionChildTree.ts`（命名以最终职责为准）。

该 hook 仅收敛单项目与多项目已经相同的子会话树职责，不触及项目分组状态：

- 输入：会话集合、当前作用域内选中的会话 key、状态 key 生成器、有效 state-key 集合、`onLoadChildren` 回调。
- 管理：`expandedChildren`、`loadingChildren`、`childrenHasMore`、`collapsedBaseCount`；在有效 key 变化时复用 `pruneChildState` 清理前三者。
- 输出：
  - `buildRows(...)`：生成 `session` / `child-toggle` 行，保留 fork 顶级规则、选中父链自动可见和 3 条上限；
  - `toggleChild(row, sessions, scope)`：保持现有“首次展开记基数 → 拉第一页；有下一页则拉下一页；否则收起”的行为；
  - `collapseChild(...)`：供 `ToggleRowButton` 明确收起；
  - 现有渲染需要的 loading、has-more 与 key helper。

**接入：**

- `SessionList.tsx`：删除本地子树 state、建树、加载与 toggle 重复代码，使用共享 hook；单项目 key 仍是 `session.key`。
- `useMultiProjectSessionList.ts`：删除同一批重复代码，使用共享 hook；多项目继续以 `nodeId + rootId + sessionKey` 建 key，确保同名项目跨节点不会串态。
- `MultiProjectSessionList.tsx` 与 `SessionList.tsx` 只保留渲染 `VisibleSessionRow`、文案和动作绑定，不再自行实现树状态机。

### 3. 类型与组件边界

- `VisibleSessionRow` 和 `COLLAPSED_CHILD_SESSION_LIMIT` 仍在现有 session-list 共享模块，避免无关的目录迁移。
- `ToggleRowButton` 保持现有 API；项目加载更多不再给它 `showCollapseIcon` 或 `onCollapse`。
- 子会话仍使用 `ToggleRowButton` 的双动作语义，因为此处“展开/加载更多/收起”确实是同一棵树的连续操作，具有独立用户价值。
- `SessionList` 与多项目列表的搜索、归档、外部导入路径不改。

## 不做

- 不移除项目名称点击收起/展开。
- 不移除子会话折叠、3 条可见上限、选中链路自动可见或子会话分页。
- 不改变默认项目展开、localStorage schema、pin 排序、会话搜索、归档、外部导入分页。
- 不引入新的“加载更多”策略或改变每页数量。
- 不把工具卡、思考块、关联文件、看板列的折叠状态纳入本次范围。

## 测试与验收

### 新增针对性测试

- 为共享树构建/状态逻辑加入 Vitest 覆盖：
  - fork 会话仍在顶级；
  - 非选中父会话不泄露子项；选中子会话的祖先链可见；
  - 折叠态最多显示 3 条；
  - 展开前基数在加载更多后仍保持，收起后剩余数不膨胀；
  - 作用域 key 不同的同名会话互不影响。
- 为项目底部行加入回归测试：
  - `remaining > 0` 时只触发加载更多；
  - `remaining = 0` 时不渲染项目底部 toggle；
  - 项目名称仍是唯一的展开/收起入口。

### 手动验收

1. 折叠项目 → 切换任意节点 → 返回：该项目保持折叠；从未手动设置的项目保持默认展开。
2. 点击项目名称：收起/展开正常；展开后若仍有未加载会话，仍会加载。
3. 项目底部只显示“还有 N 个”，加载完后消失，不再出现“收起”。
4. 选中子会话时，其父链仍可见；多于 3 个的子会话仍能展开、继续加载、收起。
5. 同名项目位于不同节点时，子会话展开态互不串扰。

### 门禁

- `cd web && npm test`
- `cd web && npx tsc --noEmit`
- `make test`
- `make check-upstream`
- 依照 `ship` 流程部署；本机服务重启仍只由用户执行。

## fork 定制登记

实施时创建一组新的 upstream customization（建议 `G-JI`）：登记项目底部职责分离、共享子会话树状态机、可见症状、why、文件、测试与锚点。提交信息带 `Scope: G-JI`。

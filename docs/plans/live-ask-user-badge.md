# ask_user 徽标实时亮灭 + 项目文件树两处交互修正

状态：Implemented 2026-10-20（实现见下方「实际实现」；待提交与 ship）。

一次做完三件事（用户 2026-10-20 明确「一次全做，一起发」）：

- **A. 问号徽标实时亮灭**（原计划主体，跨节点）
- **B. 左侧文件树侧边栏默认打开「文件」页**，不要跳 git 页
- **C. 项目名前的下拉箭头 = 纯展开/收起**（跨节点也一样）；点项目名才是进入项目

---

## A. 徽标：症状与根因

### 症状

1. 徽标亮：提问后能亮（点进对话后一定亮）。
2. 徽标灭：**回答完不灭**，要等这一轮会话结束、列表重拉才消失。
3. 跨节点：会话跑在 A 节点、浏览器停在 B 节点时，任何一侧的变化都传不到另一侧。

### 根因（已用证据确认，不是推断）

徽标（`SessionCardTopBadges`）读的是**会话列表项**里的 `aux_flags.ask_user_waiting`。

- 后端真相是对的一半：`ask_user` 提问置位写进了会话行
  （`appcontext_task_flags.go` → `updateSessionAuxFlags` → `manager.UpdateSessionAuxFlags`），
  但**清位**只挂在 `tool_call_update complete` 那条事件上 —— 那要等 agent 真的把答案
  当 tool result 收下才有。用户在卡片上按下「提交」到那一刻，库里还是「在等回答」。
- **没有推送**：aux 标志的写入路径不发任何广播。`session.meta.updated`
  （`ws_broadcast.go:72`）只在改名/建会话/子会话那几条路上发，aux 写入不在其中。
- **前端列表项是快照**：aux 变化**不改会话的 `updated_at`**，所以
  `realtimeWsSessionMeta.ts` 里按 `updated_at` 增量拉的列表请求**带不回**这条会话 ——
  列表项会一直拿着旧值。
- 跨节点更没有通道：浏览器只有**一条** WS，连的是当前激活节点
  （`sessionConnectionManager.ts`），别的节点上的会话永远推不过来。现存的跨节点活通道
  只有 5s 一次的 `/api/replying-sessions` 旁路轮询，而它带的是 `awaiting_answer`。

## 实际实现（四步）

### 1. 后端：aux 写入后广播 `session.meta.updated`（同节点立即灭）

`AppContext.updateSessionAuxFlags` 写库成功后重读会话行（`manager.GetMeta`），
调 `BroadcastSessionMetaUpdated` 发出 —— payload 里带的是**最新的** `aux_flags`。

- 覆盖置位与清位两条路（同一个写入点）。
- 不新增字段白名单：复用现成的 `BroadcastSessionMetaUpdated`。

### 2. 后端：答题那一刻就清会话行（不再等 agent 收下答案）

`ws.go` 的 `handleSessionAnswerQuestion` 在答案**提交成功**后立刻
`h.AppContext.clearSessionAskUserWaiting(rootID, key)`（新方法：
`updateSessionAuxFlags(AskUserWaiting: false)` + 广播）。

- 库里的真相当场就是「不在等」⇒ 任何一次后续列表重拉都不会把徽标点亮回来。
- 顺序仍在 `usecase.AnswerQuestion` 成功之后：提交失败（agent 不在了）时徽标要留着，
  用户才能重试。

### 3. 前端：`session.meta.updated` 就地更新列表项的 aux_flags

`realtimeWsSessionMeta.ts` 在 `session.meta.updated` 里补一段：payload 带 `aux_flags`
时，用 `setSessions` 就地把这一条列表项的 `aux_flags` 换掉（`normalizeSessionAuxFlags`
归一化）。不改这一步，第 1 步的广播到了前端也只会更新缓存，不会更新**列表项**。

### 4. 前端：跨节点靠 5s 轮询覆盖徽标

- `useReplyingSessionPoll`：轮询里**出现过的**会话记**显式** `awaiting_answer`
  （true/false 都记）。「缺席 = 不在等」会把没被轮询到的会话的徽标误清一片。
- `appSession.ts` 的 `applyPendingToMultiProjectGroups`：键**存在**才作数 ——
  存在 = 知道真相（不在等 ⇒ 徽标灭），缺席 = 不知情 ⇒ 保留库里的值。

跨节点 ≤5s 灭；同节点是毫秒级。

## B. 侧边栏默认页签

- 真相在 App 层（`useAppFileWorkspaceState` 的 `useState<ProjectTreeTab>("files")`），
  但 `FileTree` 自己从 localStorage 读初始页签，而那个键**从来没人写** ⇒ 侧边栏永远
  停在一个陈旧的 `"git"` 上。
- 改法：FileTree 的页签改成**受控**（`projectTreeTab` / `onProjectTreeTabChange` 两个
  prop），删掉死键 `PROJECT_TREE_TAB_STORAGE_KEY`；`FileTreePanel` → `AppRootView` 把
  App 层的值传下去。`projectTreeTabRequest`（切页签的请求通道）接上。

## C. 根行箭头 = 纯展开/收起

- 根行没有独立分支，跨节点时点箭头会一路落到「打开这个根」那段
  （`selectRootNode` + 换根 + `tryShowBoundSessionForRoot`）—— 表现为「收起永远不生效，
  倒像先进了项目，得再点一下才收得起」。
- 改法：`openDirectoryAction` 在 `selectRootNode` **之前**加一段根行专用分支 ——
  点箭头只改展开状态，不切当前项目、不换选中节点、不进主面板；展开时按**该根所属的
  节点**取顶层目录（缓存键 `treeKey(nodeId, root, ".")`，与文件树里根行的子节点键同源）。
- 点项目名（`onSelectRoot`）那条「进入项目」的路不动。

## 验收

1. 提问 → 徽标在**列表**里亮（不必点进对话），A、B 两个节点都能看到。
2. 回答 → 徽标立刻灭（同节点毫秒级，跨节点 ≤5s）。
3. 打开左侧文件树侧边栏 → 停在「文件」页。
4. 跨节点点项目名前的箭头 → 直接展开/收起；点项目名 → 进入项目。

## 门禁与收尾

- 测试：
  - `server/internal/api/session_aux_flags_broadcast_test.go`（aux 写入广播 / 答题清位）
  - `web/tests/ask-user-badge-live.test.mjs`（轮询合并覆盖徽标 + 列表项就地打补丁）
  - `web/tests/project-tree-tab-and-root-toggle.test.mjs`（页签默认值 + 根行箭头纯展开）
- 登记：`docs/upstream-customizations.yaml` —— G-AD 补徽标实时亮灭；新组 G-JM 收 B/C。
- `make test` + tsc + web 测试 + `make check-upstream`（**提交后**再跑，它只比已提交文件）。
- 提交信息带 `Scope: G-AD, G-JM`。
- 分发：WSL/fn 走 `scripts/deploy-all.sh`；pc-win 走手工交叉编译路径；本机由用户重启。

# 通知机制重做：三类提示音 + 完成未读徽标

状态：Implemented 2026-10-25（待 ship）。

用户 2026-10-25 原话：「彻底的调查并考虑重做我们现在的提示机制，它在任务完成和需要人介入，出错等等的时候，
都应该要发出明确清晰的提示音，并有专门设置的地方。而且我希望执行完成的任务 除了灭灯 会话还要有一个完成的
未读提示徽标 只有被查看才会消失，从而和完成很久的任务做区别。」

后续追加：「应用更加好听一点的声音」「把设置移动到左侧边栏右上角的弹出菜单里，替换掉现有的'开启通知'」。

---

## 改造前的真实状态（调查结论）

| 问题 | 事实 |
|---|---|
| 完成音 | 存在，但只有**一个单音**，唯一触发点是 `session.done`。 |
| 介入音 / 错误音 | **完全不存在**。 |
| 设置入口 | 不存在。侧栏 ⋯ 里只有 PWA 推送订阅（`WebPushMenuItem`）。 |
| 未读徽标 | 「实现了」但等于没有：v1 只按 `sessionKey` 存，且只有单项目列表 `SessionList` 读它，多项目列表 `MultiProjectSessionList` 压根没接 `unread` prop —— 多项目模式（默认开）下永远看不到。 |

后端那套推送旁路（`notify.BuildSessionPayload` / `webpush` / `notifyscript`）是完整的，
缺的只是**屏幕前**这一层：声音与未读态。

## 设计要点

**1. 三个事件，三种音型，两个维度区分**（音高 + 节奏）：
complete 上行三连 `C6→E6→G6`、intervene 双短提示 `A5×2`、error 下行纯四度 `F5→Bb4` 长拖尾。
错误音最初写的是 `A3→E3` 的低闷音，用户实测「听不见，换一个」—— 220Hz 在笔记本小喇叭上
几乎不发声，且和系统提示「咚」混在一起。改成中音区的三角波，既听得见，又靠**下行**与
完成音的上行形成方向对比。
每个音符独立 oscillator，8ms 起音 + 指数衰减防爆音。

**2. 设置存前端 localStorage**（用户选择），`mindfs.notify.sound`：总开关 + 音量 + 逐事件开关。

**3. 未读键 = `nodeId::rootId::sessionKey`**（用户选择前端本地持久化）。三个段缺一不可：
同名项目 / 同名 key 跨节点会互相点亮，这正是 v1 失效的一半原因。v1 数据直接丢弃 ——
反推不出节点与项目，迁移等于把误标带过来。

**4.「正在看的不标未读」** = `document.visibilityState === "visible"` 且选中 key 相同。
清未读挂在 `useSessionSelection.handleSelectSession`（列表点击 / 深链恢复 / 看板跳转的唯一漏斗），
且在 `setBoundSessionForRoot` 之前 —— 晚一步会先闪一下红点。

**5. 设置 UI = 侧栏 ⋯ 菜单里的「通知」折叠面板**（用户决策「合并成通知面板」）：
上半提示音设置（每个事件带「试听」，试听按钮本身就是 AudioContext 的手势解锁），
下半内嵌原 `<WebPushMenuItem />`，现有 PWA 推送能力一个不丢。

## 事件源接线

| 事件 | 触发点 | 说明 |
|---|---|---|
| 完成 | `realtimeWsHandlers.ts` 的 `session.done` | 后端回合唯一终结器。顺带按上面的判据标未读。 |
| 介入 | `realtimeWsSessionMeta.ts` 的 `aux_flags.ask_user_waiting` **false→true** | 同一个提问会随 meta 广播重复下发，只在跳变时响。必须读**覆盖前**的 `cached` 快照。 |
| 出错 | `session.error`（请求没发出去）+ `session.stream` 的 `error` 事件（agent 侧失败） | 两条通道覆盖的是不同的失败，都要响，用同一个错误音。 |

## 实际改动

新增：
- `web/src/app/useNotificationSound.ts`（原 `useCompletionSound.ts` 重写）
- `web/src/shared/unreadCompleted.ts`（放 `shared/` 而不是 `services/` —— 架构测试禁止 components → services）
- `web/src/components/file/NotificationMenuItem.tsx`
- `web/tests/notification-sound-and-unread.test.mjs`

接线：`SessionCard` 加 `unread` prop，两条会话列表都订阅 `unreadCompletedSubscribe`；
context 里的 `playCompletionSound()` 统一改成 `playNotificationSound(event)`
（4 个类型/实现文件跟着改）。

徽标位置（2026-10-25 二轮，用户问「会话上方小徽标做好了么」）：不放在右侧时间列
（那一格和「回复中/等你回答」的空心灯挤在一起，两个信号迟早读混），而是挂在
**会话名上方那排**（`SessionCardTopBadges`）最左：14px 圆角方框 + ✓，底色
`var(--node-badge-bg)`、图标色跟随节点色，与项目面包屑徽标同一套视觉（AGENTS.md §11）。
`hasSessionTopBadges` 把 `unread` 算进判据，否则「只有未读、没有其它徽标」的会话
这一排整块不渲染，徽标根本不会出现。

设置面板版式（同日二轮）：用户报「前端样式还是不统一」。改为完全沿用
`FileTreeFontSizeMenu` 的行式（外层 `padding: 2px 6px 6px`、每行 `minHeight 30px`、
12px 字号、次要信息 `var(--text-secondary)`、数值控件用同款胶囊），并接入菜单里
「同时只展开一项」的既有约定（展开时收起外观/字号/语言/排序）。

## 二轮：跨节点完成信号 + 灭灯后的时间回退（2026-10-25）

用户报两条现象，都在同一条链上：

| 现象 | 根因 |
|---|---|
| 跨节点的会话跑完，既没有提示音也没有未读徽标 | 完成音与未读徽标只挂在 `session.done` 这个 handler 里，而浏览器**只连激活节点的一条 WS**（`useAppBootstrapEffects.ts` 的 `sessionService.connect(currentRootId, getNodeIdForRoot(currentRootId))`）—— 远端节点的 `session.done` 根本不会推过来。 |
| 执行完成灭灯后，时间不变成「刚刚」，而是退回上一句完整对话的时间（例如 12m） | `session.done` 后走 `loadSessionsForRoot(..., {replace:true})`，该分支从**空数组**起算（`mergeSessionItems([], next)`），把流式期间推进的 `activity_at` 整条丢了。服务端 `updated_at` 是**最后一条对话消息**的落库时间，agent 连跑 12 分钟工具时它一直停在 12 分钟前。 |

**约束（用户明确）**：「不要额外引入新机制」。所以两处都复用已有链路，不新增通道、不动后端。

跨节点完成信号 = 读 `useReplyingSessionPoll` 已在维护的 pending 真相表前后两帧，做 true→false 差分
（`shared/pendingCompletion.ts` 的 `newlyCompletedPendingKeys`）。三条排除缺一不可：

- **节点必须本轮真的问到**（`okNodeIds`）—— 失败节点保留旧值是合并逻辑的既定行为，不能当完成；
- **激活节点不算** —— 那条节点的 `session.done` 已由 WS 响过、标过，再判一次会连响两遍；
- **空 nodeId 的历史键不算** —— 反推不出节点，标未读会落到错误的键上。

一轮最多响一次（切回标签页那一轮可能一次补出好几条，逐条会连成噪音），未读徽标仍逐条标。
「正在看的那条不标未读」复用与 WS 侧同一判据。

时间继承 = `sessionListMerge.ts` 新增 `inheritClientActivity(previous, incoming)`，在 `replace`
分支与多项目分组重建两处把上一帧的 `activity_at` 搬过来。**只搬不判**：留不留仍由
`mergeSessionItems` 那条唯一规则决定（落后于 `updated_at` 就删），两处各写一遍迟早分叉。
键按 `_nodeId::sessionKey` 作用域，跨节点同名会话不互相继承。

多项目分组那半**只能继承、不能并集**：写成 `mergeSessionItems(prevGroup.sessions, …)` 会把
服务端本轮已不返回的会话（删掉/归档）从旧分组复活。测试里有一条 `doesNotMatch` 专门钉这个。

代价（已知）：`activity_at` 是 client-only 字段不落库，**刷新页面后会回落到 `updated_at`**。
这次修的是「灭灯后当场不刷新」，刷新回落是固有边界。

## 验证

```
npx tsc --noEmit -p tsconfig.json          → EXIT 0
npx vitest run                             → 189 passed | 5 skipped（406 tests）
make check-upstream                        → 全部通过
make install                               → 产物含 unreadCompleted.v2 / soundIntervene / notificationPanel
```

新增测试做过变异验证：把 `viewing` 强制改为 `false`（取消「正在看的不标未读」），
对应断言立刻红。

## 已知未做

- 「需要介入」目前只认 `aux_flags.ask_user_waiting` 的跳变。若某个 agent 后端只发
  `session.ask_user` 而不写 aux 标志，这条不会响 —— 现状 claude/codex/acp 三条路径都写 aux，
  暂不额外兜底（多一条通道就要多一份去重）。
- 未读态不做跨标签页之外的实时同步（`storage` 事件已覆盖跨标签页）。

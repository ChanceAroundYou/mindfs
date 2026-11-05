# Reply alerts cross-node · per-session notifications

Status: Implemented 2026-11-05（v0.5.5-1111），待真机验收。

## 需求（用户 2026-11-05 原话拆解）

1. 触发时机：会话**完成**或**需要你回答（提问）**。
2. 粒度：**每个会话一条**通知，不是合并成一条汇总。
3. 点击：直接跳到那条会话。
4. 前台也要提醒：只要用户**不是正在看这条会话**，App 在前台也该弹。
5. 范围：**跨节点**。不能只轮询一个 worker，节点表里每台在跑任务的机器都要看得到——与现有「完成打勾 / 未读徽标」的覆盖范围一致。

## 现状与缺口

| 环节 | 现状 | 缺口 |
|---|---|---|
| 轮询范围 | `ReplyPollerService` 只有一个 `apiBaseUrl` | 只盯一台机器 |
| 轮询时机 | `MainActivity.onResume` 发 `ACTION_PAUSE`，前台不轮询 | 前台完全收不到提醒 |
| 通知粒度 | `showSingleReplyNotification`/`showReplyNotifications` 把状态压进 `FOREGROUND_ID` 或分组汇总 | 不是每会话一条 |
| 点击深链 | 通知带 `rootId`/`sessionKey`，`takePendingReplySession` → `handleSessionChipClick` | 不带 nodeId，跨节点会话找不到 |
| 正在看 | 无 | 无法抑制「正在看的会话」 |

Web 侧已有等价物可直接复用：`useReplyingSessionPoll` 逐节点拉
`/api/replying-sessions`（`appPath(..., nid)` 已处理节点路由与账户参数），
并用 `newlyCompletedPendingKeys` 做 true→false 差分。**原生缺的正是这一层。**

## 设计

### 数据流

```
JS（挂载 / 选中变化 / 节点表变化）
  → MindFSReplyPoller.configure({
      nodes: [{ id, url }],        // url 由 JS 用 appPath("/api/replying-sessions", nid) 生成：
                                   //   已含节点路由、user= 账户参数、挂载前缀，原生不再拼 URL
      viewing: "nodeId␟rootId␟sessionKey" | ""
    })
  → ReplyPollerService 常驻前台服务，逐 URL GET（3s 连接 / 5s 读超时），
    按 nodeId␟rootId␟sessionKey 合并成一张表
  → 状态机（每会话一条通知）
       replying → 消失        = 完成     → notify(done)
       awaiting_answer 由假变真 = 需要回答 → notify(awaiting)
       两态都跳过 viewing 命中的会话
  → 通知 extras: nodeId / rootId / sessionKey
  → 点击 → MainActivity.onNewIntent → pendingOpenSessionJSON
  → JS openReplySession({nodeId, rootId, sessionKey}) → 先切节点+项目，再选中会话
```

URL 由 JS 生成是关键：账户参数（跨节点用用户名而不是账户 id）、`/mindfs`
挂载前缀、节点路由都在 `appPath` 里已经验证过，原生一旦自己拼就会重演
「前缀被抹掉 → 404」那次事故（G-KJ）。

### 关键决定

- **通知 id** = `hash(nodeId␟rootId␟sessionKey)`。同一会话从「需要回答」变成
  「完成」时复用同一个 id：后者覆盖前者，不会叠两条。
- **常驻前台服务通知**（`FOREGROUND_ID`）继续保持极简：`MindFS` / `有会话正在回复`，
  不点名（已完成）。提醒与它是两条独立通知。
- **「正在看」的判定**：App 可见 **且** 当前选中会话 == 该 key。只在前台有效；
  切到后台后 viewing 视为空（后台不可能是「正在看」）。
- **不再 `onResume` 全清**：只在用户点开某会话时 dismiss 它自己那条
  （`handleSelectSession` 已经调 `dismissNativeReplyNotification`）。

### 改动清单

原生（`web/android/`，G-JC 前缀已登记）：
- `ReplyPollerService`：节点列表配置、多 URL 轮询、复合键、每会话一条、viewing 抑制、
  去掉单条/分组模式切换。
- `MainActivity`：`onResume` 不再暂停；`configure` 解析 `nodes`/`viewing`；
  通知 intent 带上 `nodeId`。
- `ReplyPollerPlugin`：`configure` 透传 `nodes`/`viewing`。

前端：
- `services/net/replyPoller.ts`：`syncNativeReplyPoller` 发送节点表与 viewing；
  新增 `syncNativeReplyViewing(key)`。
- `app/useAppDomainLayer.ts`：会话选中 / 可见性变化时同步 viewing；
  `openReplySession` 接受 nodeId。
- `app/useAppTaskDrawer.ts`（`handleSessionChipClick`）：支持 nodeId，切节点后再选中。

### 验收（真机）

1. 本机与 fn 各起一个长任务，App 切后台：两条任务各自弹**一条**通知（不是汇总）。
2. 点其中一条：App 打开并**切换到对应节点**的那条会话。
3. App 停在该会话上时，该会话完成 → **不**提醒；另一节点那条仍提醒。
4. 提问（awaiting）同样一条独立通知；同一会话回答后再完成，只更新不叠加。
5. 无任务时通知栏无残留；点开某会话只消它自己那条。

## 风险

- 常驻前台服务带来的耗电（每 5s × 节点数 次请求）。先按 5s，观察后可退到 10s。
- 跨节点 `handleSessionChipClick` 依赖目标项目能被选中；若节点离线，深链要给出
  明确提示而不是静默失败。

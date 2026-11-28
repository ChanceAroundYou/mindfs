# 思考块尾部残留与页脚归属：完成即重锚

> Scope IDs: G-SL（claimed 2026-11-28T05:09Z）

## 背景与取证

- 截图会话：fn 节点 `CMAI / most申报优化 / 1795694836-79c10cf6ca5d`，回合 `seq 670`。
- fn 的权威块流按 `pos` 排：`reasoning(264, 1400 字)` 在 `text(265–267)` 之前；
  原始 transcript 同一 message id 的 thinking（02:47:54.212Z）也早于正文（02:47:56.298Z）。
- 截图（10:51，回合结束 4 分钟后）却显示正文 → 旧页脚 → 思考块；说明客户端停在
  **流式瞬时行**的旧状态，没有用持久化块流重建该轮。
- 跨节点（浏览器只连当前激活节点的 WS）的 `session.done` 不会发到查看远端会话的客户端。
  唯一跨节点完成通道是 `useReplyingSessionPoll` 的 5s 轮询；它现在只负责完成音/未读，
  选中会话的 true→false 甚至被 `continue` 跳过，从未触发重锚。
- 页脚问题：旧实现把整轮页脚挂到「该轮最后一条带 turnMeta 的 item」，而瞬时 thought
  行没有 turnMeta，`isAssistantFlowEnd` 会把它当成下一轮开始，页脚因此停在思考块上方。
  已完成的任务 task-137（G-SK）把页脚拆成「完整轮次后的控件行 + 常驻状态条」，
  瞬时行不再参与页脚归属；本任务在其上补齐「完成即重锚」。

## 改动范围

1. `web/src/shared/pendingCompletion.ts`
   - 新增纯函数 `selectCompletedSelectedSession(completedKeys, selected)`：
     从「任一节点刚完成」的 scoped key 里挑出当前选中会话（key 必须一致，
     两侧都有节点身份时节点也必须一致）。
2. `web/src/app/useReplyingSessionPoll.ts`
   - 完成差分（`completedAnyNode`）算出后，若命中选中会话，回调
     `onSelectedSessionCompleted({ nodeId, rootId, sessionKey })`。
   - 回调走 ref，引用变化不进入 `refresh` 依赖（避免 5s 轮询被重启成死循环）。
3. `web/src/app/useSessionLifecycle.ts`
   - 新增 `reanchorSession(rootId, key, nodeId?)`：与 WS done 重放同一条序列
     （restoreActiveSession → loaded/stale 收尾 → 选中态换新缓存 → drawer），
     让 viewer 按 `_anchoredAt` 原子换窗。
   - `restoreActiveSession` 接受可选节点身份：显式 `_nodeId` 优先于 root→node
     反推，并把节点身份写进缓存/回包，后续 `toSessionItem` 不丢。
4. `web/src/app/useAppSessionCore.ts` / `useAppSessionLayer.ts` /
   `useAppMultiProjectReload.ts` / `useSessionSelection.ts`
   - 把 `reanchorSession` 接到轮询的完成回调；选择会话时同样透传节点身份。
5. 回归测试 `web/tests/thinking-tail-completion-reanchor.test.mjs`
   - 纯函数：完成键与选中会话的匹配（key + node，root 反解）。
   - 行为：`pending=false` 的组装丢弃 seq=0 瞬时行（含尾部 thought），
     `pending=true` 必须接回；控件行锚点仍是完整轮次的最后一条正文。
   - 源码契约：轮询完成分支确实调用重锚；节点身份显式传递；层间接线完整。

## 验收

- `npx tsc --noEmit`、相关 Vitest、`go test ./server/internal/api/usecase ./server/internal/agent/claude ./server/internal/session` 全绿。
- `make check-upstream` 通过（本组登记 + Scope 标记）。
- 不复现截图状态：远端会话结束后 ≤5s（一个轮询周期）从持久化块流重建，
  思考块回到正文之前，控件行落在该轮最后一条正文之后。

## 排除

- 不改后端块流写入顺序（已按真实起点正确）。
- 不改 Claude 思考事件的发送时机（无法从 transcript 恢复流式顺序；以持久化权威序收口）。
- 不重启本机服务；不推送、不部署。
# fn 上 codex 续聊变成新会话（G-KI）

**状态**：Implemented 2026-11-04，待 ship。

## 现象

在 fn 节点用 codex 发消息，后续消息不断开新会话而不是续接当前对话（用户原话：
「老是打开新的会话，而非继续对话」）。

## 取证

1. 不是 codex 后端问题：fn 上 `thread/resume` 正常，同一 `agent_session_id` 的
   `agent_ctx_seq` 连续递增（4→8→…→86）。
2. 服务端只在 `session_key` 为空时新建（`server/internal/api/ws_session_message.go`）。
   用户报的那些「新会话」每条 `agent_ctx_seq=2`（单轮），说明客户端发的是空 key。
3. 客户端「续哪条」只认内存表 `boundSessionByRootRef`（`prepareSendMessageTarget.ts`），
   查不到就直接造 `pending-<ts>` 草稿 = 新会话，没有任何回落与日志。

## 根因

**「输入框发给谁」有两个真相源**：用户看到的 `selectedSession` 与内存里的
`boundSessionByRootRef`。发送路径只信后者；后者一旦丢失/漂移就静默新建会话，
而转录还在屏幕上。两条丢失路径：

- **P-1 草稿未提升**：新会话靠 `session.accepted` 把 `pending-*` 提升成真键。WS 断开
  （fn 是远端节点、PWA 切后台必断）导致回包丢失时，绑定永远停在 `pending-*`；而
  `resolveLockedSessionKey()` 把 `pending-*` 一律当作「没有绑定」，于是之后每条消息都新开。
- **P-2 作用域键漂移**：`selectRootNode(root)` 不传 `nodeId` 时无条件清空
  `currentRootNodeIdRef`，`node::root` 作用域键当场漂移，绑定/选中/抽屉/cache 一起变孤儿。

## 修复

1. **发送端兜底**：绑定缺失/解析不到时，续接主面板正在显示的那条会话并回写绑定
   （`resolveInputSession` 语义保留：抽屉持有输入时不劫持）。`pending-*` 不再静默
   `return null`，改为显式 `blocked: "draft_key_missing"` 并提示用户。
2. **作用域键稳定**：同 root 未传 `nodeId` 时沿用 `prevNid`；跨节点切换路径不变。
3. **幂等重放自愈**：服务端 `requestID → {seenAt, sessionKey}`，重复请求只回键、
   不重复跑回合；客户端在 `ws.connected` / `ws.reconnected` 用原 requestId 重放孤儿草稿
   （最多 3 次），拿到 `session.accepted` 后照常提升为真键。

## 测试

- `web/tests/send-target-display-fallback.test.mjs`
- `web/tests/root-node-scope-stability.test.mjs`
- `web/tests/pending-draft-recovery.test.mjs`
- `server/internal/api/ws_request_reservation_test.go`

## 验证

门禁：`go test ./...`、`tsc --noEmit`、`npm test`、`go run ./scripts/check-upstream`。
真机：fn 上同一会话连发 3 条，服务端日志连续 `resume_runtime_session`、同一 key，
`agent_ctx_seq` 递增且 `session_agent_bindings` 只有一行。

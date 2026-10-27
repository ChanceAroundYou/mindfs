# 幽灵会话：只把「真回合」的 ResultMessage 当回合边界

状态：Implemented 2026-10-27，待 ship。

## 症状（用户 2026-10-27 报）

pc(wsl) 节点 `core` 项目会话「裤腰底部优化 / #1」（key `1793005653-4478d98a7bf8`）：

- 只在输出正文时亮灯；一进 tool 调用/等上游大模型，输出一停就灭灯报完成；结果回来又亮。
- **任何阶段按停止都打断不了**这一轮。
- 用户的原话里有一条关键限定：「大多数对话运行起来并非这个状态，只有非常少数的会话出现」。

## 证据链（WSL 全量 journal 2026-10-01 起 888,198 行，2026-10-27 取证）

1. 该会话的 claude 进程活着，转录持续增长 —— 不是进程死了、不是会话目录被删。
2. `ResultMessage` 共 1103 条，其中 **19 条带 `origin.kind = "task-notification"`**
   （`duration_ms` 38–13496、`usage` 全 0、`subtype: "success"`）；
   其余 1084 条真回合 result **不带 origin 字段**。全库只有这一种非人类 origin。
3. 这 19 条里有 **11 条在同一秒内立刻触发 `[ws] session.done`**，散落在 9 个会话里，
   其中 7 个确实被误结束过。绝大多数会话从未产生过它 —— 正好对上「只有非常少数」。
4. 触发条件：会话里用了 `run_in_background`。后台任务完成时 CLI 会把一条 notification-origin
   的 result **插在新一轮的开头**（实测 11:23:30：`input "This session was migrated…"`
   → `system/init` → `result origin=task-notification dur=65` → `session.done` 同一秒；
   真正的 result 直到 12:11:33 才到）。
5. `resultErr` 对 `subtype:"success"` 返回 **nil** ⇒ `SendMessage` 成功返回
   ⇒ `runSessionMessage` 的 defer ⇒ `EndSessionTurn` ⇒ 广播 `session.done` + `ClearSessionPending`，
   而 CLI 里的真回合还在跑。
6. 灯的表现是派生：`realtimeWsSessionStream.ts` 的 `markStreamPending()` 对除
   `message_done`/`error` 外的一切流事件重新点亮，只有 `session.done` 熄灯 —— 于是「有 chunk 才亮」。
7. **停止失效也是派生的**：假 done 之后 `activeTurns` 已清空，`ws_upgrade.go` 取到的
   `target_turn=0` 被 `staleCancelTarget`（G-BE 的闸门）恒丢弃。实测 11:25:41–11:26:21
   连续 11 次 `target_turn=0` 全部空转。

## 两处被推翻的既有判断

- **不是「存量数据被写坏」**：交换日志里的 `content:""` 是 S2d-2 的设计（正文由 `blocks.jsonl` 承载），
  seq 62/63/64 完好；`persist turn blocks: context canceled` 是服务重启所致，与本缺陷无关。
- **不是 G-BE 没修好，也不是 G-IZ 合成子代理**：G-BE 拦的是「迟到的 done 抹掉**新轮**」，
  这里是「**伪造的** done 提前结束**当前轮**」。worktree 在、`external_source_path` 为空、进程活着。

## 修法（G-KG）

`server/internal/agent/claude/` 两处：

```go
// session_turn.go，紧邻 resultErr
func isTurnBoundaryResult(msg claudeagent.ResultMessage) bool {
    if msg.Origin == nil { return true }
    return strings.EqualFold(..., string(claudeagent.MessageOriginKindHuman))
}
```

`session_message_loop.go` 的 `case ResultMessage` 收成一次 `s.handleResultMessage(m, raw)`：
边界分支保持原行为；**非边界整条跳过** —— 不补正文、不发 MessageDone、不推进 context window、
不结束本轮、不动 `sawDelta`/`sawMessageText`，只多记一行
`[agent/claude] result.ignored session=%s origin=%q subtype=%q result_len=%d`。

两处取舍的理由：

- **白名单而非黑名单**：`MessageOriginKind` 有 6 个值（human / channel / peer / task-notification /
  coordinator / auto-continuation），黑名单只挡今天碰巧出现的那一个，下一种 kind 一出现就原样复发。
  未知 kind 一律按非边界处理：宁可让那一轮继续跑（仍在 `activeTurns` 里，**停止按钮可用**），
  也不要提前广播 done。
- **正文整条跳过而不是润出去**：子任务输出本来就由 `TaskNotificationMessage → tool_call_update`
  卡片呈现；润成正文会被 `persistTurnBlocks` 落成孤立句插进用户这一轮的回答里，
  且那条 result 的 `usage` 全 0、context 却从 `modelUsage` 推出 200000，挂到助手行上是错的。

**明确不做**：不给 `TargetTurnID == 0` 开逃生口。它是本缺陷的结果而非原因；要「猜对」需要 adapter
暴露「CLI 手里是否还有一轮」的新状态，而那层状态今天不存在，硬加会把 2026-10-02
「停止掐掉刚起来的新轮」的回归放回来（`HasActiveUse` 也救不了：假 done 后 refcount 已归 0）。
修完边界后 `activeTurns` 非空，取消走原路即生效。

## 验证

- `session_result_origin_test.go` 四条：非边界（5 个已知非人类 kind + 一个未来新 kind）不发光 done/chunk
  且 waiter 仍挂起；边界（无 origin / human）发光 done 并弹一个 waiter 且复位 saw 标志；
  通知先到不弹 waiter、真回合只弹第一个；非人类 origin 的错误 result 也不塞错误给本轮。
- 变异验证：`isTurnBoundaryResult` 恒 `true` ⇒ 测试 1/3/4 红；恒 `false` ⇒ 测试 2/3 红。两条都实测过。
- `make test` 全绿、`make check-upstream` 绿（G-KG 登记完整）。
- 线上回看口径（部署后）：`journalctl --user -u mindfs | grep -A2 'result.ignored'`
  不应再出现紧随其后的 `session.done`，且静默期小蓝灯保持亮。

## 与 G-BE 的关系

G-BE 立的不变量是「`session.done` 只能由一个收尾编排路径决定」（`docs/message-mechanisms.md` §7.4 第 5 条）。
本条补的是它的上游：**什么才算「一轮结束」**。两条一起才能守住那个不变量 ——
没有 G-KG，收尾路径本身会被伪造的边界触发；没有 G-BE，迟到的真 done 又会抹掉新轮。

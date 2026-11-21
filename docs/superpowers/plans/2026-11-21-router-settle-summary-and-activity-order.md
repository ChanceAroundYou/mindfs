# Router 结算总结 + 提问卡位置 + 结算失败补偿

Plan 2026-11-21。用户报告三件事（`/refine`）：

> 1. #99 worktree 汇报太长了没有总结
> 2. llmux 下面的任务，也呈现出几个问题，1. 提问卡片沉底，它没有按照实际应该出现的位置，2. 出错重试的留了一个提示在底部 但成功跑完以后没有去做总结

用户后续裁定：

- #99 在 **mindfs** 项目、**本机**节点；llmux 是那**两个待审核**任务（#19 / #20）。
- 「llmux 的请直接看吧」—— 授权只读查库。
- #99 的「没有总结」与 llmux 的第 2 条是**同一个需求**：每一轮成功跑完都必须有一段总结。
- **总结由 Router 统一写**，但 Router 不是全程只说一次话：
  > 我不赞同 这个应该是完整的一段agent总结回复 只是分成6个assistant块。我希望由router统一写。但是不是说全程router只能说这么一次话，它可以多次看会话进度，给出简单进度汇报留痕。但是最后要有一段长度合理的总结。
- 历史补齐：**补齐（推荐）** —— #99、llmux #19、llmux #20。
- 重试预算：「timeout到300吧 超时可以重试，给router的限制放宽一点 不要太吝啬次数」→ `router.turn_timeout` 300s（配置，已改本机）、事件回合总尝试 2 → 3。

## 线上取证（只读）

| 现场 | 证据 | 根因 |
|---|---|---|
| mindfs #99 `task_37edf6984579253e`（waiting_user） | `result_summary` = 2870 字，是节点回复原文六块直接相接（「…changes.Now update `mergeVanishedSource`…」） | ① 回复正文逐块 `WriteString`，中间的工具调用没有留下段落分隔；② `result_summary` 由 `workflowReplySummary` 取最后一个成功节点输出，结算后只有 25s 的异步压缩窗口，慢模型（`ok` 别名首字节 178s）必然超时，保持原文 |
| llmux #20 `task_f3de197705b988a7` | 提问卡 `created_at` 晚于回复占位，列表按 `created_at` 升序 → 卡片落在回复下面 | 回复占位在回合开始就落库，中途产生的提问卡创建时间更晚；排序只看创建时间 |
| llmux #19 `task_260f5740cced704f` | `router_events` 里 `run_settled` 事件 **failed**，`last_error="persist turn blocks: context deadline exceeded"`；压缩后的 434 字总结写进了活动，但结算回合的汇报没进对话 | 事件回合撞 180s 死线失败后不重放，任务最终成功，用户看到的最后一行是「出错重试」提示 |

## 改动（S1–S5）

### S1 结算总结由 Router 写（`purpose=summary`）

- `router_say` 的 `purpose` 枚举加 `summary`（`router_tools.go` + schema 校验）。
- 事件回合的 purpose 归一化不再把 `summary` 改写成 `progress`（`router_coordinate_turn.go`）。
- 只有 `brief.trigger.kind=run_settled` 的回合保留 `summary`；别的回合模型误写 `summary` 时在 `markRouteReply` **降级成 progress** —— 汇报照常发出去、只是不写任务结果摘要，不让一条措辞错误毁掉整轮（`activity_route_apply.go`）。
- `applyRouteSay` 遇到 `purpose=summary` 时**无条件**写 `tasks.result_summary`：任务这时可能已经是 success/waiting_user，`recordReplySummaryIfActive` 在终态上不写，所以不能走那条兜底。
- `router_prompt.md` 新增规则 18：结算总结四段（结论 / 证据 / 没做或未验证 / 下一步），200–400 字、上限 800；只写有证据的东西；一个结算回合一条 summary，其余回合用 progress。

### S2 回复正文按段落拼接（6 块不再黏连）

新增 `server/internal/api/turn_text_blocks.go::appendTurnTextBlock`：两段助手文本之间夹了工具调用/思考/待办/计划/压缩事件时补一个空行，并吃掉下一块自带的前导换行。分隔规则与流式累积同源 —— `usecase.appendResponseChunk` 改名为导出 `AppendResponseChunk`，workflow 回合直接复用，不另立一套。

### S3 提问卡按完成时间排

`activityOrderBy`：**已完成的回复**按 `updated_at`（完成时间）排，其余行按 `created_at`。未完成的回复没有可依的完成时间，仍留在创建位置，避免回合进行中的时间线跳动；`updated_at` 为空的历史行回落到 `created_at`。

### S4 压缩窗口跟上慢模型

`runSummaryCompressTimeout` 25s → 90s（`runSummaryCompressMaxRunes` 600 → 800）。压缩仍是锦上添花：超时/空/过长就保持原文，不做无界等待。

### S5 结算失败的事件补一次

新增 `router_settle_compensation.go::requeueFailedSettleEvents`，挂在 `coordinateTick` 上：

- 只补 `kind=run_settled`、窗口 7 天内、`last_error` 属于可重试分类的 failed 事件，每拍最多 5 条。
- 重新排队时 `attempt` 归零、`turn_id` 加补偿前缀 `c:` —— 否则会命中旧回合 `(task_id, turn_id)` 的幂等行，模型根本不会再被调用一次。
- 补偿前缀同时是「同一事件只补一次」的闸：列表侧过滤避免它每拍占掉一个名额，UPDATE 侧再判一次防竞态。

### 附：事件回合重试预算 2 → 3

`routerEventTurnMaxAttempts = 3`（用户「不要太吝啬次数」）。`router.turn_timeout` 本机 180s → 300s（配置文件，不入库）。

## 测试与变异

| 用例 | 钉住什么 | 变异（打红后还原） |
|---|---|---|
| `TestRunSettledSummaryWritesTaskResultSummary` | 结算回合 `purpose=summary` 写进 `tasks.result_summary`，回复行 `purpose=summary` | 去掉 `applyRouteSay` 的 summary 写入 → 红 |
| `TestFailureEventSayIsNeverASummary` | 失败回合的 summary 降级成 progress，任务摘要不动 | 去掉 `markRouteReply` 的降级 → 红 |
| `TestFailedSettleEventIsRequeuedForSummary` | 补偿重排 → 消费 → 写总结；同一事件补过一次后再失败不再重排 | 同时去掉列表与 UPDATE 两道补偿前缀闸 → 红 |
| `TestActivityStore_ListOrdersCompletedRepliesByCompletion` | 完成的回复按完成时间落在提问卡之后 | 排序退回只看 `created_at` → 红 |
| `TestActivityStore_ListKeepsPendingRepliesAtCreation` | 未完成回复留在创建位置 | 同上 → 红 |
| `turn_text_blocks_test.go` | 工具调用前后补空行、同块不重复补 | 去空行 → 红 |

## 状态

**已实现，门禁见提交记录；未部署、未重启任何节点。** Scope: G-NX。

剩余：
- 历史补齐（#99 结果摘要、llmux #19 的 failed `run_settled` 由 S5 下一拍自动补、llmux #20 的排序靠前端重拉即可）。
- 本机需要用户 `sudo systemctl restart mindfs` 才生效；worker 由 `scripts/deploy-all.sh` 分发。

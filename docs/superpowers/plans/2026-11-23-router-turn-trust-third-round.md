# Router 回合可信化第三轮：巡检回话 · OOB 恢复 · 摘要与失败判定 · 块流读取

Plan 2026-11-23。起点是用户观察「现在的回复次数有点太多了，进度和汇报好像老是重复」，要求
「全部涉及到的需要改动的点都做深度修复」。只读取证之后列出 6 类问题（C1/C2/H1/H2/M1/M2），
外加一个超出审核范围的性能问题（块流膨胀）。用户对 8 条编号清单逐条裁定：第 1/2/3/4/5/6/8 条
按建议执行，第 7 条（块流）由「单独立项」改为**本轮一起处理**。

## 1. 批准的口径

| # | 项 | 决定 |
|---|---|---|
| 1 | C1 巡检回合动作范围 | 只放开 `say / ask_user`，`complete_task` 维持原限制 —— 巡检不该在用户没说话时自行闭合任务 |
| 2 | H1 是否回填历史 | 不回填，只对新写入生效 |
| 3 | M1 抑制粒度 | 同回合已有 summary 时完全抑制 progress，但保留活动正文 |
| 4 | M2 判定严格度 | 首行 + 已知签名（`prompt is too long` / `context length` / `api error` / `error:`）即判失败 |
| 5 | C2 持久化落点 | `router_task_state` 加列，不用 task_events 事件反推 |
| 6 | C2 恢复阈值 | 30 分钟 |
| 7 | 块流膨胀 | 本轮处理（覆盖「单独立项」的原建议） |
| 8 | 二进制 | 先 `make build && make install`，不重启，由用户择时重启 |

## 2. 六项修复（Scope: G-PX）

| 提交 | 问题 | 修复 |
|---|---|---|
| `b73d8781` | H1 摘要不截断 | 落库层统一截断到 600 字（`TaskStore` 与 `ActivityStore` 两个写入点），不再只靠调用方 |
| `6d0bf607` | H2 失败轮被写成结论 | 失败回合不再把兜底文案 `recordTurnResultSummary` 写进 `tasks.result_summary` |
| `d31f3aae` | M2 判定过松 | harness 错误行后跟长正文时仍判失败（原先只看整段是否以错误签名开头） |
| `76ddd367` | H2 失败不可见 | `turn_settled` 事件带失败标记与原因 |
| `104f89e4` | M1 进度淹没总结 | 结算回合里总结之后的进度汇报不再重复渲染（同回合已有 summary 时完全抑制 progress，活动正文保留） |
| `593760db` | C1 巡检无消费者 | 巡检回合没有来源留言时也能回话（`say / ask_user` 放开，`complete_task` 仍拒） |
| `fdff1e25` | C2 任务卡 running | OOB 会话回合跨重启恢复成待审核：标记落 `router_task_state` 三列，启动对账 + 每分钟后台扫描在 30 分钟后写 `turn_interrupted` 并放回 `waiting_user` |

C2 的落库口径：`MarkOutOfBand` / `ClearOutOfBand` / `LoadOutOfBand` / `ListStaleOutOfBand`
（`router_task_state.go`），恢复在 `service_recover_out_of_band.go`，`BeginOutOfBandSessionTurn`
成功改状态之后落标记、`restoreOutOfBandTask` 开头先清标记。恢复动作的顺序是**先写事件、后改状态**，
中途崩溃下一拍会重来且事件按 `oob:<task>:<started>` 幂等。终态/已归档任务只清标记不改状态。
6 条新用例（`router_out_of_band_recovery_test.go`）+ 6 处变异各自打红；`go test
./server/internal/kanban/` `ok 308.608s`，`server/app`、`server/internal/api` 全绿。

## 3. 块流读取流式化（Scope: G-PY，`baca9439`）

**取证**：`.mindfs/sessions` 共 768 MB；最大单文件 70,712,496 B / 45,693 行 / 528 seqs，
单行最大 289 KB。膨胀不是重复内容（重复仅 932 行 / 0.4 MB），而是超长会话本身
（28,041 个 tool_call、13,991 个 reasoning）。真正的代价在读路径：发送路径每轮
`CountBlocksAfterSeq` 把整份文件读进内存 → `strings.Split` → 逐行 `json.Unmarshal`，
实测 **685.6 ms / 157 MB 分配**；崩溃恢复时 `SealProvisionalTurnBlocks` 还要跑两遍全量读。

**实现**：`blocks_stream.go` 提供 `forEachBlockLine`（bufio 复用行缓冲、超长行按需扩容、
不截断）与 `blockSeqFromLine`（行首 `{"seq":` 前缀快路径，不碰正文）；`readBlocksAll`、
`CountBlocksAfterSeq`、`SealProvisionalTurnBlocks` 全部改走它，封口合并成一次扫描同时拿
未封口块与各 pos 最大 revision。实测 **59.0 ms / 75 MB（约 11.6×）**，峰值内存只跟最长一行走。

**坏行语义必须与旧实现逐项等价**，因此两处显式判：
1. **没有换行结尾的末行不回调** —— `AppendBlocks` 每块都写 `payload + '\n'`，读到 EOF 却不是
   换行收尾只能是崩溃留下的半行。不丢的话半行里的 `{"seq":4,` 会被前缀解析认成合法 seq。
2. **前缀快路径要求行尾是 `}`** —— 块行是 `json.Marshal` 出的紧凑对象；真实文件 45,693 行
   全部以 `}` 收尾（实测 0 例外）。旧实现靠整行解码失败顺带跳过坏行，流式路径没有这个副作用。

3 条新用例（`blocks_stream_test.go`，覆盖前缀/回退/坏行、2 MiB 超长行 + 截断尾行、真实
AppendBlocks 后追加密半行的计数），3 处变异（去行尾校验 / 放行半行 / 计数改 `>=`）各自打红后还原。

## 4. 剩余

- **存量块流/aux 数据：用户裁定「不动」**（770 MB：块流 367.5 MB + aux 421.7 MB + 其他约 13 MB）。
  不归档、不压缩、不删除、不重写；本轮未改动 `.mindfs/sessions` 中任何文件。读路径已快 11.6×，
  存储体量对磁盘无压力，保持原样是零风险选择。
- `make build && make install` 后**不重启**；本机 `sudo systemctl restart mindfs` 由用户择时执行。
- 线上验收（摘要可用率、OOB 恢复可见性）待重启后取窗口。

## 5. 边界

- 本轮只读审核出的 6 类问题全部落在上述提交里；没有改 Router 提示词、没有动 `complete_task` 的既有闸门。
- 块流改动是纯读取路径优化：写入格式、文件布局、`fullBlockReads` 计数器语义均未变。
- 未部署任何 worker；本轮只 push 与本地编译（用户已授权），不 ship、不重启。
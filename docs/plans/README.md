# Plans index

This is the only index for execution plans. A plan is active only when listed under
**Active** below. Historical plans retain their execution notes and are not instructions
to resume their old TODOs or baselines.

> 历史部署 / 发布记录已搬到 [DEPLOY_HISTORY.md](DEPLOY_HISTORY.md)，按需读取；本文件只保留执行计划索引与规则。

> Active 条目的完整执行记录与 Completed 表已搬到 [PLAN_HISTORY.md](PLAN_HISTORY.md)，按需读取；本文件只保留可执行的 Active 链接与规则。

## Active

- [指令注入预算根除：AGENTS.md / plans 索引瘦身 + 口径校准](../superpowers/plans/2026-11-24-instruction-budget-eradication.md)
- [上下文溢出压缩成功后自动重发（复用原回合，有界一次）](../superpowers/plans/2026-11-24-overflow-compact-auto-resend.md)
- [溢出压缩被用户重试打断：自动重发闭环修复](../superpowers/plans/2026-11-24-overflow-compact-resume-interrupted.md) — Plan 2026-11-24，Scope: G-QU。承 G-QT 本机重启后的真实失败：CLI 在 191 ms 内以 `blocking_limit` 本地拒发（`input_tokens=0`，代理没有对应请求），G-QT 正确登记重发并排 `/compact`，但旧文案「完成后请重新发送」促使用户点停止再发，`session.cancel` 把压缩回合 abort，`finishHygieneCompact` 记失败并清掉重发待办；harness 后来报成功也没人重发，胶囊还残留「压缩没生效」。三处修复：已排压缩的文案改为「完成后会自动重发，无需手动发送」（保留 `blocking_limit` 机器判据）；pinned 用户取消在压缩 pending 时拒绝并说明原因（内部无 pinned 目标的取消不受影响）；压缩成功清掉旧 `compact_error`。三条 RED→GREEN，三处变异各自因断言失败打红；全量 Go 与前端门禁实跑全绿。**未 push、未部署、未重启任何节点**：本机运行进程已由用户重启到 `v0.5.5-1400-25-g74ceff29`，本修复上线后仍需在真实长会话复验。
- [工作流会话活跃判定 · 显式 Run 启动事件 · 回合结算的 Run 证据](../superpowers/plans/2026-11-24-workflow-session-liveness-and-turn-evidence.md)
- [移动端工作流两级弹层避让](../superpowers/plans/2026-11-24-workflow-selector-mobile-placement.md)
- [Router 巡检回合放行 notify_user / escalate](../superpowers/plans/2026-11-24-router-inspection-supervision-apply.md)
- [过期任务提示与 root 错误可诊断性修复](../superpowers/plans/2026-11-24-stale-task-prompt-and-root-error.md)
- [边控端（Edge Controller）：握手 · 安装包 · 自更新](../superpowers/plans/2026-11-23-edge-controller-handshake-and-installer.md)
- [Router OOB 活跃判定 · 提问卡取代 · 巡检回合可见拒绝 · 摘要保留决策](../superpowers/plans/2026-11-23-router-oob-liveness-question-settle-and-summary.md)
- [Scope 组号治理：脚本申请、门禁拦截、撞号裁决](../superpowers/plans/2026-11-23-scope-id-allocation.md)
- [Router 回合可信化第三轮：巡检回话 · OOB 恢复 · 摘要与失败判定 · 块流读取](../superpowers/plans/2026-11-23-router-turn-trust-third-round.md)
- [配额挂起收口 · 飞书显式目标确认 · 跨 harness 一致性订正](../superpowers/plans/2026-11-22-intake-target-confirm-and-quota-hold.md)
- [Live compact 收尾：压缩竞态 · 计费口径 · 校准护栏 · 诊断字段](../superpowers/plans/2026-11-22-live-compact-compact-race-calibration-and-billing-metric.md)
- [会话流式记录 A+：未收尾回合的 provisional 块流与中断恢复](2026-11-22-session-stream-provisional-blocks.md)
- [Router 收尾可信化：每轮可读总结 · 任务级巡检 · 跟进会话](../superpowers/plans/2026-11-22-router-turn-settlement-supervision-and-session-follow.md)
- [超长会话提示：久 × 大 三档 + N 轮后自动触发 harness 压缩](../superpowers/plans/2026-11-21-session-hygiene-long-session-hint.md)
- [离线优先的本地控制面（Edge Control Replica）](../superpowers/plans/2026-11-21-offline-first-local-control-plane.md)
- [Super agent 升级：节点级管理员 + 自设计工作流](../superpowers/plans/2026-11-21-super-agent-node-admin.md)
- [飞书通道 M1：本机出口与点击](../superpowers/plans/2026-11-21-feishu-channel-m1-outbound.md)
- [飞书通道 M2 + M4：消息入口与主控中转](../superpowers/plans/2026-11-21-feishu-m2-intake-and-m4-relay.md)
- [Router 结算总结 + 提问卡位置 + 结算失败补偿](../superpowers/plans/2026-11-21-router-settle-summary-and-activity-order.md)
- [任务树 + 节点树：真建、真合、w2 → w1 → main](../superpowers/plans/2026-11-21-worktree-task-and-node-nesting.md)
- [证据门 + 自动补做](../superpowers/plans/2026-11-20-evidence-gated-autonomous-completion.md)
- [项目身份 = (节点, rootId)：不同节点的同名 / 同路径项目互不串](../superpowers/plans/2026-11-20-project-identity-across-nodes.md)
- [Router 主动监管 + 2026-11-20 复审五项修复](../superpowers/plans/2026-11-20-router-supervision-and-review-fixes.md)
- [Live Compact 通用内核 + Codex 适配器](../superpowers/plans/2026-11-20-live-compact-universal-fold-core.md)
- [任务侧输入框：@ 候选与粘贴截图（看齐会话输入框）](../superpowers/plans/2026-11-20-task-composer-mentions-and-pasted-images.md)
- [工作流节点间传递 {previous_output} + 节点级 Plan 模式 + 手机草稿抽屉](../superpowers/plans/2026-11-20-workflow-previous-output-and-mobile-drafts.md)
- [任务 #86 卡死：unknown 算已收尾 + 启动对账补投影 + Router 读会话](../superpowers/plans/2026-11-20-router-unknown-settled-and-read-session.md)
- [任务会话收拢：一轮 Run 一条会话 + 跨轮 Router 选 new/continue + 侧栏按任务分组](../superpowers/plans/2026-11-20-task-session-consolidation.md)
- [Router 回合失败不丢动作 + 超时可见 + brief 状态跟随投影](../superpowers/plans/2026-11-19-router-turn-timeout-salvage.md)
- [一轮失败不封板 + 终态任务可回到待审核](../superpowers/plans/2026-11-19-task-reopen-and-failed-round-review.md)
- [Router 收尾死锁：已结算的 Run 不再算「活动执行」](../superpowers/plans/2026-11-19-router-settled-run-livelock.md)
- [用户答复回填停住的 Agent 节点](../superpowers/plans/2026-11-19-answer-waiting-node.md)
- [Super agent R0/R1 工头执行计划](2026-11-18-super-agent-r0-r1.md)
- [任务审批收尾：末端 gate 禁定义、审批卡可读化、附言交给 Router](../superpowers/plans/2026-11-19-task-gate-and-reports-unfold.md)
- [MindFS 原生上下文活压缩（Live Compact）](../superpowers/plans/2026-11-18-mindfs-native-live-compact.md)
- [安卓后台回复提醒：常驻通知可点 + 近实时推送](../superpowers/plans/2026-11-18-android-background-push.md)
- [Task Router（任务工头）设计](2026-11-14-task-router-super-agent.md)
- [Workflow UI unification and template migration](../superpowers/plans/2026-11-11-workflow-ui-unification-and-template-migration.md)
- [Conversation-driven task implementation](../superpowers/plans/2026-11-12-conversation-driven-task-implementation.md)
- [Agent settings unified](agent-settings-unified.md)
- [Codex and MindFS configuration sync](../superpowers/plans/2026-11-09-codex-mindfs-bidirectional-sync.md)
- [Agent-first workflow authoring and reference-pattern migration](../superpowers/plans/2026-11-11-kanban-agent-first-workflow-authoring.md)
- [Kanban-first agent workflows — follow-up roadmap](../superpowers/plans/2026-11-10-kanban-workflow-runtime-roadmap.md)
- [Kanban-first agent workflows](../superpowers/plans/2026-11-09-kanban-agent-workflows.md)
- [HTML preview runtime upgrade](html-preview-runtime-upgrade.md)
- [Image zoom and file auto refresh](image-zoom-and-file-auto-refresh.md)
- [去上游化：抹掉 agent9 痕迹 + iPad 套壳](de-brand-and-ios-shell.md)

## Plan rules

- Do not infer that a plan is active from its filename or an unchecked list inside it.
- Keep one active plan per coherent goal; link it here and state its status in this index.
- Record implementation results in the plan, then update this index in the same change.
- Historical documents may keep old measurements when clearly labeled as historical evidence.

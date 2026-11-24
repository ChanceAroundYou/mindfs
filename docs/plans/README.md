# Plans index

This is the only index for execution plans. A plan is active only when listed under
**Active** below. Historical plans retain their execution notes and are not instructions
to resume their old TODOs or baselines.

> 历史部署 / 发布记录已搬到 [DEPLOY_HISTORY.md](DEPLOY_HISTORY.md)，按需读取；本文件只保留执行计划索引与规则。

> Active 条目的完整执行记录与 Completed 表已搬到 [PLAN_HISTORY.md](PLAN_HISTORY.md)，按需读取；本文件只保留可执行的 Active 链接与规则。

## Active

- [Web Push 只在页面还开着时投递（关闭页面后不再弹桌面通知）](../superpowers/plans/2026-11-24-web-push-presence-close-page.md) — Plan 2026-11-24，用户报「PWA 在页面/界面关闭后仍持续弹桌面通知，多个设备关掉页面后通知还不停往外蹦」。只读取证否掉了「页面内定时器 / 重复投递 / SW 里 `clients.matchAll()` 静默忽略」三个猜测（`userVisibleOnly` 订阅下 SW 不 `showNotification` 会被 Chrome 换成兜底提示）：真根因是 Web Push 通道与页面生命周期无关，而订阅表永久、扇出全量、事件源是后台 agent 回合（worker 经 `/api/web-push/relay` 中继到主节点代发）。修复：订阅新增粘性 `PresenceCapable`；进程内 `PresenceTracker`（endpoint → client_id → 心跳，TTL 10 分钟，每 endpoint 16 客户端 / 全表 512 endpoint 上限）；`sendToItems` 对 capable 订阅要求心跳，全部被滤掉时返回 nil 而非报错；`POST /api/web-push/presence`（心跳 / pagehide beacon）；`SendTestToEndpoint` 豁免；旧订阅 fail-open（未升级的客户端不会被误静音）。前端 30s 心跳 + `pagehide` `sendBeacon`（text/plain Blob）+ `pageshow` 恢复，SW 零窗口自愈（仍照常显示通知）。测试 19 条（presence 5 / 扇出 6 / API 4 / 前端契约 4），10 处变异各自打红后还原；隔离实例 7481 + 假 push 端点 7482 复现 7/7 PASS（旧订阅照发 → presence 订阅关页 0 投递 → 心跳恢复 → leave 立即静默 → 测试豁免 → 多标签页不连坐），生产 7331 全程只读。未部署、未重启任何节点，真机浏览器点验待部署后做。Scope: G-RA（原 claim 号 G-QU 与 main 的 6f34fd4c 撞号，合入前避让改号）。
- [hasee 边控端：状态可查 · 安装器快捷方式 · 局域网可达 · 自动切换 · 升级](../superpowers/plans/2026-11-24-hasee-edge-controller-five-items.md) — Plan 2026-11-24，用户在 hasee（Windows 边控端，`role=control` + `control_sync.role=edge`，`:7332`）真机装好并配对成功后提五条需求，并批准按 D1–D6 推荐执行（原话「全部按你推荐」）：Windows 防火墙入站放行（源限局域网）+ nginx vhost、不做主控中转；书签只留边缘地址、`online` 时顶层导航 302 到主控（**API / WS 不跳转**）；卸载只删快捷方式 / 计划任务 / 程序文件、保留 `config.json` 与数据；主节点 status 返回只读摘要（不含凭据与载荷）；升级顺序 hasee → 主控端 → 前端，**本机 `sudo systemctl restart mindfs` 仅用户执行**；中继不实现。**P0（Scope: G-QV）已完成**：主节点 `/api/control-sync/status` 原本恒 404 —— 装配只给边缘建 `ModeController`，handler 与路由都因 `h.ControlSync == nil` 短路；新增 `controlsync.PrimaryStatus` DTO + `ControlSyncPrimaryStatus` 抽象 + 主节点路由兜底，返回 `role=head_rev/edge_count/active_edge_count/last_edge_seen_at/conflict_count`，三项计数任一失败整体失败、已踢边缘不计数。**P1（Scope: G-QW）已完成**：`setup.ps1` 新增 `Install-Shortcuts`（COM 建 `%APPDATA%\...\Start Menu\Programs\MindFS.lnk`，target 装好的 exe、**参数留空**、`Save()` 后 `Test-Path` 复核），`install.ps1` 安装建同一条、`-Uninstall` 删掉它（保留 `%APPDATA%\mindfs`）。变异验证 7 处（删 `Save()` / 追加 autostart 旗标 / 目标拼成 `.bak` / 去掉落盘复核 / 删调用点 / 卸载不删 `.lnk`）各自打红后还原，`go test -count=1 ./internal/deploy/` 全绿。P2（节点表 + 防火墙 + nginx，纯运维）、P3（边缘在线自动跳转）、P4（升级）未动工，组号在实施时另行 claim。**未 push、未部署、未重启任何节点。** Scope: G-QV / G-QW。
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

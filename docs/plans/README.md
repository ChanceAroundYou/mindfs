# Plans index

This is the only index for execution plans. A plan is active only when listed under
**Active** below. Historical plans retain their execution notes and are not instructions
to resume their old TODOs or baselines.

## Active

- [Session child tree consolidation](session-child-tree-consolidation.md) — Implemented 2026-10-18，待提交与 ship；单/多项目子会话树已收束到共享 hook，保持选中链路、3 条上限、分页与收起。
- [Live ask_user badge](live-ask-user-badge.md) — Implemented 2026-10-20，待提交与 ship；徽标亮灭实时（aux 写入广播 + 答题即清 + 轮询带真相），并入侧边栏默认「文件」页、根行箭头纯展开、页签不许自动联动三处修正。
- [File browser row action menu](file-entry-row-menu.md) — Implemented 2026-10-20，待提交与 ship；主区文件列表逐条三点菜单（删除/重命名/移动），复用既有控件与 /api/file/operation，弹层 portal + 锚定定位。
- [HTML and video preview](html-video-preview.md) — Implemented 2026-10-22，已合入 main，待 ship；路径式原始资源端点（账户段进路径，相对资源才不掉账户），HTML 沙箱 iframe 渲染 + 源码切换，视频直连后端 Range 播放。
- [Composer chips and Codex limits](composer-chips-and-codex-limits.md) — Implemented 2026-10-22，已合入 main，本机已安装；WSL/fn 待分发；徽标带改流内一行（被排队消息顶起来）、内存徽标左置并对所有 agent 显示且跟随项目节点、Codex 补 5 小时限额且剩余与重置时间合并；后续两轮修正：限额靠左、重置 0 隐藏、重置时间按窗口语义分开写（5h→HH:MM、周限→剩 Md Nh）、胶囊文案去重（30%%/重置 重置）、胶囊去掉「重置」二字并压短名称（5h/周）。


## Completed or superseded history

| Plan | Status | Current interpretation |
|---|---|---|
| [Long session load perf](long-session-load-perf.md) | Implemented 2026-10-22，待提交与 ship | 大会话打开/上翻冻结已收口。真机 A/B：上翻最坏长任务 2,608 ms → 112 ms、DOM 78,351 → 856、上翻一页载荷 9.36 MB → 57 KB、首屏窗口请求 2 → 1 次。三批：窗口块数预算 + 窗口 reasoning 去重（G-JS）→ 时间线虚拟化（G-JT）→ 首屏请求合流（G-JU）。 |
| [Delegated presence and flicker](delegated-presence-and-flicker.md) | Completed 2026-10-17（含同日收尾补记） | A 委托态空心灯 + 切换首帧去闪 `eafa86b3`(G-AY)、B 合成子代理可取消 `da320483`(G-IZ)、C 等回答空心灯 `a33f630b`(G-JA) 全部落地并登记。收尾补记：当时记的「SDK 适配缺口」经适配层真机测试推翻（卡片本来就能到、能渲染、能 done），真缺陷是「卡片可见 → waiter 登记」约 74ms 窗口内答案被拒导致该轮永久卡死，已随 G-JA 修复。 |
| [File size hard-line cleanup](file-size-hard-line-cleanup.md) | Completed 2026-10-16 | 4 个超硬线文件拆完（web/tests ×3 + codex importer），`file-size.md` 适用范围补上 `web/tests`，500 行门禁扩到 `.mjs` 并变异验证过。全门禁绿。 |
| [Session header menu and repoint](session-header-menu-repoint.md) | Completed 2026-10-16 | ⋯ menu shipped + deployed (G-JB); gate fully green (go + tsc + 349 web tests + check-upstream). |
| [Frontend test migration and rule hardening](frontend-test-rule-hardening.md) | Completed 2026-10-14 | All frontend tests use Vitest; frontend/backend rules and gates apply without path-based waivers. Full gates and upstream coverage passed; no-restart ship installed matching artifacts locally and on WSL/fn. No node was restarted. |
| [Frontend export and filename alignment](frontend-export-naming.md) | Completed 2026-10-14 | Naming docs and AST gates now agree; standalone components/hooks match filenames, `.tsx` contains JSX, and `.ts` does not. Cohesive multi-export modules remain responsibility-named. Frontend tests, typecheck, build, and upstream coverage passed. |
|---|---|---|
| [App.tsx under 1000](app-tsx-under-1000.md) | Completed 2026-10-09 | Final execution record; `App.tsx` is now a small composition root. |
| [Rule completion rollout](rule-completion-rollout.md) | Completed 2026-10-14 | Agent-neutral rules and plan entrypoints are verified; frontend and backend TODOs are complete; component-to-service imports are zero. Commit `84c0bada` passed all gates, upstream customization coverage, and no-restart ship to local/WSL/fn. No node was restarted. |
| [App.tsx next split](app-tsx-next-split.md) | Completed 2026-10-08 | Historical phase record; its original size baseline was superseded by the next plan. |
| [Deep split phase 3](deep-split-phase3.md) | Completed / superseded | Historical target and execution record; do not use its 2026-10-07 baseline. |
| [Deep split phase 2](deep-split-phase2.md) | Completed / superseded | Historical phase record. |
| [Frontend split refactor](frontend-split-refactor.md) | Historical | Preserves execution details; its embedded TODO and measurements are historical. Use `docs/agent/rules/frontend/TODO.md` for current frontend tasks. |
| [App.tsx split lessons](app-tsx-split-lessons-and-next-plan.md) | Historical | Preserves lessons; proposed next steps and file counts are superseded. |
| [Next-stage decision](next-stage-decision.md) | Executed / superseded | The proposal and session split were carried out; its original baseline is not current. |
| [Worktree finish stage](worktree-finish-stage-plan.md) | Implemented | The described workflow landed; this document is the design and implementation record. |
| [Agent governance and task-38 repoint](agent-governance-and-repoint.md) | Superseded 2026-10-14 by user direction | Governance work completed. The user explicitly discontinued task-38 repoint; do not resume that work from this historical plan. |
| [Backend file size 300/500 and folder reorg](backend-file-size-300-500.md) | Completed 2026-10-16 | B1–B10 完成后端 Go 文件（含测试）全部降到硬线 500 以内；`codex/importer_tool_calls.go` 的拆分边界单独论证过（见 backend/TODO.md），当天晚些的硬线清零轮把最后两个例外也拆了，现在全仓无超线文件。两处钉文件名的源码守卫改为扫包级并用变异验证过。 |

## Plan rules

- Do not infer that a plan is active from its filename or an unchecked list inside it.
- Keep one active plan per coherent goal; link it here and state its status in this index.
- Record implementation results in the plan, then update this index in the same change.
- Historical documents may keep old measurements when clearly labeled as historical evidence.

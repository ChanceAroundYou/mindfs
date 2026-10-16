# Plans index

This is the only index for execution plans. A plan is active only when listed under
**Active** below. Historical plans retain their execution notes and are not instructions
to resume their old TODOs or baselines.

## Active

| [Delegated presence and flicker](delegated-presence-and-flicker.md) | Active | Delegated-parent hollow presence + switch-flash fix in progress |
| [Session header menu and repoint](session-header-menu-repoint.md) | Active | Session title-bar ⋯ menu (change directory / rename / archive / delete) in progress |


## Completed or superseded history

| Plan | Status | Current interpretation |
|---|---|---|
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
| [Backend file size 300/500 and folder reorg](backend-file-size-300-500.md) | Completed 2026-10-16 | 除 `codex/importer_tool_calls.go`（边界已论证，见 backend/TODO.md 的「不再拆」条目）外，后端 Go 文件（含测试）全部降到硬线 500 以内。B1–B10 完成；两处钉文件名的源码守卫改为扫包级并用变异验证过。 |

## Plan rules

- Do not infer that a plan is active from its filename or an unchecked list inside it.
- Keep one active plan per coherent goal; link it here and state its status in this index.
- Record implementation results in the plan, then update this index in the same change.
- Historical documents may keep old measurements when clearly labeled as historical evidence.

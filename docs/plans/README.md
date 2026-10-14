# Plans index

This is the only index for execution plans. A plan is active only when listed under
**Active** below. Historical plans retain their execution notes and are not instructions
to resume their old TODOs or baselines.

## Active

| Plan | Status | Next action |
|---|---|---|
| [Rule completion rollout](rule-completion-rollout.md) | Active | 前端 500 行硬线已清零，`usecase/session.go` 实测 1455 行并已 ship。`manager.go` 的 search、exchange、aux、pending tool-call、agent binding、SQLite、list、alias、meta 行读写、related-files、exchange identity、lifecycle 与窗口读取职责分域；父文件实测 749 行，低于 800 行软线。`server/internal/api/http.go` 的 Git、file/directory 与 session handler 已按域下沉（当前 867 行，低于硬线；repoint handler 留原位未改）；之后依次处理其他 6 个大文件、cli/server 双入口审计与 reviewer checklist 门禁。 |

## Completed or superseded history

| Plan | Status | Current interpretation |
|---|---|---|
| [App.tsx under 1000](app-tsx-under-1000.md) | Completed 2026-10-09 | Final execution record; `App.tsx` is now a 16-line composition root. |
| [App.tsx next split](app-tsx-next-split.md) | Completed 2026-10-08 | Historical phase record; its 2957-line baseline was superseded by the next plan. |
| [Deep split phase 3](deep-split-phase3.md) | Completed / superseded | Historical target and execution record; do not use its 2026-10-07 baseline. |
| [Deep split phase 2](deep-split-phase2.md) | Completed / superseded | Historical phase record. |
| [Frontend split refactor](frontend-split-refactor.md) | Historical | Preserves execution details; its embedded TODO and line counts are stale. Use `docs/agent/rules/frontend/TODO.md` for remaining frontend cleanup. |
| [App.tsx split lessons](app-tsx-split-lessons-and-next-plan.md) | Historical | Preserves lessons; proposed next steps and file counts are superseded. |
| [Next-stage decision](next-stage-decision.md) | Executed / superseded | The proposal and session split were carried out; its original baseline is not current. |
| [Worktree finish stage](worktree-finish-stage-plan.md) | Implemented | The described workflow landed; this document is the design and implementation record. |
| [Agent governance and task-38 repoint](agent-governance-and-repoint.md) | Superseded 2026-10-14 by user direction | Governance work completed. The user explicitly discontinued task-38 repoint; do not resume that work from this historical plan. |

## Plan rules

- Do not infer that a plan is active from its filename or an unchecked list inside it.
- Keep one active plan per coherent goal; link it here and state its status in this index.
- Record implementation results in the plan, then update this index in the same change.
- Historical documents may keep old measurements when clearly labeled as historical evidence.

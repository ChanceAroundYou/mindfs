# Plans index

This is the only index for execution plans. A plan is active only when listed under
**Active** below. Historical plans retain their execution notes and are not instructions
to resume their old TODOs or baselines.

## Active

| Plan | Status | Next action |
|---|---|---|
| [Rule completion rollout](rule-completion-rollout.md) | Active | `useAppStateLayer.ts` 已降至 498 行并从硬线清单销项；refs/cache、root selection、session/task setup 与 file/workspace state 均按域拆到专属 hooks。硬线剩余 1 项；`useAppViewComposition` 已拆为 433 行的组合 hook 与独立参数类型/工作台组合模块；`useSessionLifecycle` 为 404 行、`useManagedRoots` 为 480 行、`useSendMessage` 为 422 行。`useSessionStreamCache` 已拆至 473 行。`realtimeWsHandlers` 已拆至 426 行。`SessionPane` 已拆至 58 行壳层和 458 行内容组合。`SessionViewer` 已降至 499 行。`useMarkdownViewer` 已降至 445 行。`sessionConnectionManager` 已降至 472 行。下一步复核并拆 `useSessionStream`。 |

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

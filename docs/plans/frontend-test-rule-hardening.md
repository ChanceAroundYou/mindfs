# Frontend test migration and rule hardening

## Goal

Run all frontend tests through Vitest, reconcile frontend rules and reviewer gates, and remove path-, history-, or task-specific waivers. Keep test assertions and product behavior intact. Do not change the App composition root or restart local systemd.

## Work list

1. Audit frontend rule documents, checklists, test fixtures, and gates for exemptions, stale measurements, and conflicting instructions.
2. Migrate stored frontend tests to Vitest in small batches, preserving assertions, mocks, skips, and logical source reads; remove runner-only compatibility code.
3. Make mandatory frontend rules apply consistently to stored and new code. Resolve detected conflicts by correcting the rule or the source, never by introducing an exception list.
4. Align the rule entrypoints, checklists, TODO, test runner, plan index, and upstream customization registry. Keep historical measurements only where they are clearly historical evidence.
5. Validate links, entrypoints, rule checks, plan status, and diffs. Run the matching full repository gates and complete the ship workflow without restarting local systemd.

## Acceptance

- `npm test` runs Vitest as the only frontend test runner; no Node test lane or runner-specific hooks remain.
- Frontend rule documents and executable guards agree and do not use path- or history-based exemptions.
- Current TODOs and plan index reflect the actual state without stale fixed counts.
- Full repository gates and upstream customization coverage pass, and the final ship record confirms local systemd was not restarted.

## Progress

- Completed the runner migration: all stored frontend tests are now discovered by Vitest, and the Node runner hooks were removed.
- Replaced the component-to-service debt allowlist with a zero-edge guard and applied the shared source-size rule to every frontend file.
- Removed the backend AppContext usage-count ratchet and applied the shared Go source-size rule to all Go files.
- Rewrote current rule entrypoints and checklists to remove task waivers, path-based exceptions, WARN-only mandatory requirements, and stale current-state counts.
- A full Vitest run exposed two test-contract issues: the old export-name assertion conflicted with cohesive multi-export component modules, and the moved task styles were counted twice by a source-map-aware inventory test. The obsolete assertion was removed after clarifying the universal naming rule; the inventory now compares unique public exports. Focused Vitest checks pass.
- Remaining: complete documentation and registry review, run all repository gates, then perform the no-local-restart ship workflow.

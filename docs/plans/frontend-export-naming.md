# Frontend export and filename alignment

Status: Completed 2026-10-14

## Goal

Make frontend naming rules, their automated checks, and the existing source agree. Apply name matching only to standalone component and hook entry files. Responsibility modules may export multiple related symbols and are named for their responsibility; export count alone is not a violation.

## Scope

- Clarify naming and testing documentation; remove claims that are not implemented.
- Add AST-based naming checks for standalone component entries, hook entries, JSX helper modules, and existing suffix rules. Include fixtures or source assertions for valid multi-export responsibility modules.
- Align the inspected single-purpose source modules and their imports with the clarified naming rules, including the back-navigation hook/primitives boundary.
- Review the ten modules from the audit. Split only where the responsibility boundary is clear; document cohesive multi-export modules as valid without adding path-based waivers.
- Keep `web/src/App.tsx` unchanged.
- Update source-map tests, upstream customization records, and frontend TODO/index state as needed.

## Execution

1. Update rule and test documentation, then implement and run the focused AST naming test.
2. Rename/split the clearly misnamed modules in small batches, preserving exports and runtime behavior; update all imports and source-map mappings.
3. Re-review the remaining multi-export modules for cohesion and record the decision in this plan.
4. Run `npm test`, `npm run typecheck`, and `npm run build`; inspect the full diff and customization coverage.
5. Mark this plan complete and remove the temporary TODO only after all checks pass.

## Acceptance

- Automated naming assertions match the written naming rules and reject new violations without a path-based grandfather list.
- Valid multi-export support modules, services, and type modules are not rejected merely for lacking an export matching the filename.
- All in-scope imports resolve; tests, typecheck, and production build pass.
- `App.tsx` is unchanged; no unrelated behavior changes are included.
- Upstream customization registration and targeted tests cover the changes.

## Findings and decisions

- Initial audit found documentation claiming a filename/main-export check that the test did not implement. The universal interpretation would flag many valid responsibility, service, and type modules, so it is explicitly not the rule.
- Initial source review identified three single-purpose mismatches (`SessionViewerAttachments`, `SessionViewerAuxiliaryTimeline`, `SessionViewerToolItem`) and a mixed hook/primitives module (`useBackNavigation`). The other six listed modules are to be evaluated by responsibility cohesion, not export count.
- Expanded AST review found two additional standalone component files whose only component export had a different name (`SessionViewerSlashCommand.tsx` and `Toast.tsx`); both are included in this cleanup.
- The `.tsx` semantic scan found four files without JSX (`appMisc.tsx`, the `app/taskIcons` compatibility facade, the retired `NodeSwitcher` shim, and `EditorBridge`). They are changed to `.ts`, and the gate now checks both directions: `.ts` has no JSX and `.tsx` has JSX.
- `AgentSelectorParts` mixed visible selector pieces with model-name/error parsing used by different consumers, so the pure functions moved to `agentSelectorUtils.ts`. The remaining five audited multi-export modules are cohesive responsibility families (Markdown code rendering, file-operation menus, session cards, token/context views, scheduled-task controls); they remain grouped and are not split solely to reduce export count.

## Completion record

- Aligned the naming rule, test rule, and reviewer checklist. The AST gate checks standalone component/hook entry names and the `.ts`/`.tsx` JSX contract without imposing a universal same-name-export requirement.
- Renamed standalone components and render helper modules; moved browser-history primitives to `services/platform/backNavigation.ts` and retained React wiring in `hooks/useBackLayer.ts`.
- Reviewed all ten originally listed modules plus additional AST findings. Five cohesive multi-export modules remain grouped by responsibility; no path-based exemption list was added.
- `npm test`: 162 test files passed, 4 skipped; 324 tests passed, 5 skipped.
- `npm run typecheck`: passed. `npm run build`: passed (Vite emitted its existing large-chunk advisory).
- `make check-upstream`: passed; 1142 delta files covered by 228 groups.
- Commits: `c889beb2`, `02817b0a`.
- `web/src/App.tsx` was not changed. The pre-existing untracked root `package-lock.json` was left untouched.

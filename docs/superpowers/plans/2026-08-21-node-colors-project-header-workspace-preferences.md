# Node Colors, Project Header, and Fixed Workspace Preferences Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make node colors deterministic and migrate saved values, synchronize the non-session project header color, and permanently enable multi-project sessions and hidden files while simplifying the left settings menu.

**Architecture:** Keep Vite's native content-addressed output unchanged: a source-content change yields a new hash, while unchanged content retains the same hash. `nodeRegistry.ts` remains the node-color source of truth; `App.tsx` passes each root's color into the shared `DefaultListView`; `FileTree.tsx` removes configuration UI for preferences that `App.tsx` now fixes on.

**Tech Stack:** React 19, TypeScript 5.5, Vite 5, Node `assert`, Playwright source-level test scripts.

## Global Constraints

- Do not alter the existing uncommitted `handleSelectSession` sync fix in `web/src/App.tsx`.
- Do not randomize Vite asset names; retain content hashes.
- Do not add dependencies.
- Do not restart `mindfs.service`; the user owns service restarts.
- Desktop and mobile must use the same root-color data path.
- Use Conventional Commits with Chinese descriptions if committing.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `web/src/services/nodeRegistry.ts` | Canonical palette and deterministic persisted-color migration. |
| `web/src/components/DefaultListView.tsx` | Shared file/task header; receives and displays root color. |
| `web/src/components/FileTree.tsx` | Project-tree text emphasis and settings-menu removal. |
| `web/src/App.tsx` | Fixed preference values and root-color prop wiring. |
| `web/tests/node-colors-and-workspace-preferences.test.mjs` | Source-level regression tests for all requested UI contracts and Vite content hashing. |
| `web/vite.config.ts` | Existing content-hash configuration inspected by regression test; no expected production change. |

### Task 1: Add a failing regression contract

**Files:**
- Create: `web/tests/node-colors-and-workspace-preferences.test.mjs`
- Read: `web/src/services/nodeRegistry.ts`
- Read: `web/src/components/DefaultListView.tsx`
- Read: `web/src/components/FileTree.tsx`
- Read: `web/src/App.tsx`
- Read: `web/vite.config.ts`

**Interfaces:**
- Consumes: source files as UTF-8 text.
- Produces: a Node test executable with `node web/tests/node-colors-and-workspace-preferences.test.mjs`.

- [ ] **Step 1: Write the failing test**

```js
import assert from "node:assert/strict";
import fs from "node:fs";

const read = (file) => fs.readFileSync(new URL(`../src/${file}`, import.meta.url), "utf8");
const nodeRegistry = read("services/nodeRegistry.ts");
const defaultList = read("components/DefaultListView.tsx");
const fileTree = read("components/FileTree.tsx");
const app = read("App.tsx");
const vite = fs.readFileSync(new URL("../vite.config.ts", import.meta.url), "utf8");

assert.match(nodeRegistry, /PALETTE = \["#3b82f6", "#f59e0b", "#7c6bd6"/);
assert.match(nodeRegistry, /const PREVIOUS_PALETTE = \["#7c6bd6", "#c9b84a"/);
assert.match(nodeRegistry, /const OLD_PALETTE = \["#6d5bcf", "#0ea5a0"/);
assert.match(defaultList, /rootColor\?: string/);
assert.match(defaultList, /data-onboarding="project-home"[\s\S]*color: String\(rootColor \|\| ""\)\.trim\(\) \|\| "var\(--root-badge-text\)"/);
assert.match(fileTree, /fontWeight: isManagedRootNode \? 600 : 400/);
assert.doesNotMatch(fileTree, /fileTree\.onboarding/);
assert.doesNotMatch(fileTree, /fileTree\.showHiddenFiles/);
assert.doesNotMatch(fileTree, /fileTree\.multiProjectSessions/);
assert.doesNotMatch(fileTree, /fileTree\.swapSidebars/);
assert.match(app, /const showHiddenFiles = true;/);
assert.match(app, /const multiProjectSessionsEnabled = true;/);
assert.doesNotMatch(app, /setShowHiddenFiles/);
assert.doesNotMatch(app, /setMultiProjectSessionsEnabled/);
assert.match(app, /<DefaultListView[\s\S]*rootColor=\{[^}]*\._nodeColor \|\| null\}/);
assert.match(vite, /defineConfig\(/);
assert.doesNotMatch(vite, /entryFileNames:\s*[^\n]*Date\.now/);
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `node web/tests/node-colors-and-workspace-preferences.test.mjs`

Expected: FAIL because the current palette begins with `#7c6bd6`, the header has no `rootColor` prop, preferences are stateful, and menu controls remain.

- [ ] **Step 3: Do not edit production code in this task**

Keep the failure as the RED baseline for Tasks 2–4.

- [ ] **Step 4: Commit the test only if the project convention permits red commits**

Do not commit a failing test on its own unless the user explicitly requests an incremental commit.

### Task 2: Migrate the node palette deterministically

**Files:**
- Modify: `web/src/services/nodeRegistry.ts:3-4, 161-179`
- Test: `web/tests/node-colors-and-workspace-preferences.test.mjs`

**Interfaces:**
- Consumes: persisted `mindfs_nodes` colors from the immediate preceding palette `#7c6bd6`, `#c9b84a`, `#6a8dc2`, `#c66a7a`, `#8a8f99`, `#7aae8a` and older palette values.
- Produces: `PALETTE[0] === "#3b82f6"`, `PALETTE[1] === "#f59e0b"`, `PALETTE[2] === "#7c6bd6"`; `getNodes()` rewrites either recognized prior-palette value by its position.

- [ ] **Step 1: Extend the test with exact migration mapping assertions**

```js
assert.match(
  nodeRegistry,
  /const PREVIOUS_PALETTE = \["#7c6bd6", "#c9b84a", "#6a8dc2", "#c66a7a", "#8a8f99", "#7aae8a"\]/,
);
assert.match(nodeRegistry, /const OLD_PALETTE = \["#6d5bcf", "#0ea5a0", "#e07a2f"/);
assert.match(nodeRegistry, /const paletteIndex = \[PREVIOUS_PALETTE, OLD_PALETTE\]/);
```

- [ ] **Step 2: Run the test to verify it fails for the expected old palette declaration**

Run: `node web/tests/node-colors-and-workspace-preferences.test.mjs`

Expected: FAIL at the palette assertion; no other production code has changed yet.

- [ ] **Step 3: Implement the minimal palette and migration update**

```ts
export const PALETTE = ["#3b82f6", "#f59e0b", "#7c6bd6", "#c66a7a", "#8a8f99", "#7aae8a"] as const;
const PREVIOUS_PALETTE = ["#7c6bd6", "#c9b84a", "#6a8dc2", "#c66a7a", "#8a8f99", "#7aae8a"] as const;
const OLD_PALETTE = ["#6d5bcf", "#0ea5a0", "#e07a2f", "#2f8f4e", "#d9466a", "#7a9a3a"] as const;
```

Keep the migration keyed by palette position for both `PREVIOUS_PALETTE` and `OLD_PALETTE`, mapping any matched stored color to `PALETTE[index]`. Do not change `nextColor`, `makeLocalNode`, or add a second persistence key.

- [ ] **Step 4: Run the focused test**

Run: `node web/tests/node-colors-and-workspace-preferences.test.mjs`

Expected: palette checks pass; header/menu/preference checks still fail.

- [ ] **Step 5: Commit this independently reviewable change**

```bash
git add web/src/services/nodeRegistry.ts web/tests/node-colors-and-workspace-preferences.test.mjs
git commit -m "fix: 调整节点默认配色并迁移旧配置"
```

### Task 3: Synchronize project header color and tree text weight

**Files:**
- Modify: `web/src/components/DefaultListView.tsx:46-75, 192-215, 358-375, 416-445`
- Modify: `web/src/App.tsx:13146-13200`
- Modify: `web/src/components/FileTree.tsx:2337-2402`
- Test: `web/tests/node-colors-and-workspace-preferences.test.mjs`

**Interfaces:**
- Consumes: `rootColor?: string | null` sourced from `managedRootByIdRef.current[currentRootId]?._nodeColor`.
- Produces: the shared `DefaultListView` `project-home` badge renders node color when supplied; ordinary tree entry labels retain 400 weight when selected.

- [ ] **Step 1: Add/retain failing assertions for root-color wiring and tree-label weight**

```js
assert.match(defaultList, /rootColor\?: string \| null/);
assert.match(defaultList, /color: String\(rootColor \|\| ""\)\.trim\(\) \|\| "var\(--root-badge-text\)"/);
assert.match(fileTree, /fontWeight: isManagedRootNode \? 600 : 400/);
assert.match(app, /rootColor=\{\(managedRootByIdRef\.current as any\)\[String\(currentRootId \|\| ""\)\]\?\._nodeColor \|\| null\}/);
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `node web/tests/node-colors-and-workspace-preferences.test.mjs`

Expected: FAIL because `DefaultListViewProps` does not include `rootColor`, the badge has no color override, and FileTree makes selected children bold.

- [ ] **Step 3: Add the minimal shared header prop and wire it from App**

```tsx
// DefaultListViewProps
rootColor?: string | null;

// DefaultListView destructuring
rootColor = null,

// data-onboarding="project-home" style
color: String(rootColor || "").trim() || "var(--root-badge-text)",

// App DefaultListView call
rootColor={(managedRootByIdRef.current as any)[String(currentRootId || "")]?._nodeColor || null}
```

Do not create a mobile-only prop or duplicate header component.

- [ ] **Step 4: Make only managed-root labels emphasized in FileTree**

Replace the row's selected-state font weight with:

```tsx
fontWeight: isManagedRootNode ? 600 : 400,
```

Replace the nested name span's unconditional weight with:

```tsx
fontWeight: isManagedRootNode ? 600 : 400,
```

This preserves project-root emphasis and makes every project-internal file or directory label normal weight even while selected.

- [ ] **Step 5: Run the focused test to verify it passes the header and tree assertions**

Run: `node web/tests/node-colors-and-workspace-preferences.test.mjs`

Expected: palette, root-color, and tree-weight assertions pass; fixed-preference/menu assertions still fail.

- [ ] **Step 6: Commit the reviewable visual consistency change**

```bash
git add web/src/App.tsx web/src/components/DefaultListView.tsx web/src/components/FileTree.tsx web/tests/node-colors-and-workspace-preferences.test.mjs
git commit -m "fix: 统一项目标题与树节点主题色样式"
```

### Task 4: Permanently enable preferences and remove their menu controls

**Files:**
- Modify: `web/src/App.tsx:1346-1349, 2315, 2547-2552, 4592-4635, 4700-4706, 13551-13635`
- Modify: `web/src/components/FileTree.tsx:107-162, 1302-1345, 2876-2966`
- Test: `web/tests/node-colors-and-workspace-preferences.test.mjs`

**Interfaces:**
- Consumes: existing code paths conditional on `showHiddenFiles` and `multiProjectSessionsEnabled`.
- Produces: both values are compile-time `true`; no settings-menu control or callback can set either false.

- [ ] **Step 1: Add/retain failing source-level assertions**

```js
assert.match(app, /const showHiddenFiles = true;/);
assert.match(app, /const multiProjectSessionsEnabled = true;/);
assert.doesNotMatch(app, /setShowHiddenFiles/);
assert.doesNotMatch(app, /setMultiProjectSessionsEnabled/);
for (const label of ["fileTree.onboarding", "fileTree.showHiddenFiles", "fileTree.multiProjectSessions", "fileTree.swapSidebars"]) {
  assert.doesNotMatch(fileTree, new RegExp(label.replace(".", "\\.")));
}
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `node web/tests/node-colors-and-workspace-preferences.test.mjs`

Expected: FAIL because App has `useState` setters and FileTree still renders all four labels.

- [ ] **Step 3: Replace both preference state values with constants in App**

```tsx
const multiProjectSessionsEnabled = true;
// Remove the storage-writing effect for MULTI_PROJECT_SESSION_STORAGE_KEY.

const showHiddenFiles = true;
```

Remove `setMultiProjectSessionsEnabled`, `setShowHiddenFiles`, their local-storage initialization/effect, and FileTree callback props that solely toggle those values. Keep downstream `if (multiProjectSessionsEnabled)` checks: with the constant they retain the existing successful execution path while avoiding a broad refactor.

- [ ] **Step 4: Remove only the specified FileTree menu items and props**

Remove:

```tsx
onStartOnboarding?: () => void;
onShowHiddenFilesChange?: (show: boolean) => void;
sidebarsSwapped?: boolean;
onSidebarsSwappedChange?: (enabled: boolean) => void;
multiProjectSessionsEnabled?: boolean;
onMultiProjectSessionsChange?: (enabled: boolean) => void;
```

Then remove their destructured parameters and the four corresponding menu buttons. Retain unrelated menu controls, including appearance, locale, sorting, Git diff layout, update, notification, and agent controls. Keep `sidebarsSwapped` state in `App.tsx` if `AppShell` still consumes it; only remove the menu plumbing that allows changing it.

- [ ] **Step 5: Run the focused test to verify it passes**

Run: `node web/tests/node-colors-and-workspace-preferences.test.mjs`

Expected: PASS with all source-level assertions.

- [ ] **Step 6: Run TypeScript checking to catch removed-prop call sites**

Run: `npm run typecheck --prefix web`

Expected: PASS.

- [ ] **Step 7: Commit the preference simplification**

```bash
git add web/src/App.tsx web/src/components/FileTree.tsx web/tests/node-colors-and-workspace-preferences.test.mjs
git commit -m "refactor: 固定会话与隐藏文件显示偏好"
```

### Task 5: Verify content hashing and full frontend behavior

**Files:**
- Test: `web/tests/node-colors-and-workspace-preferences.test.mjs`
- Read: `web/vite.config.ts`
- Generated: `web/dist/index.html`, `web/dist/assets/index-*.js`

**Interfaces:**
- Consumes: Vite's default Rollup asset naming and generated distribution output.
- Produces: evidence that changed source has a different content-addressed entry and the frontend builds cleanly.

- [ ] **Step 1: Strengthen the regression test for Vite content-hash policy**

```js
assert.match(vite, /defineConfig\(/);
assert.doesNotMatch(vite, /entryFileNames:\s*[^\n]*(?:Date\.now|Math\.random)/);
assert.doesNotMatch(vite, /chunkFileNames:\s*[^\n]*(?:Date\.now|Math\.random)/);
```

- [ ] **Step 2: Run all focused web test scripts**

Run:

```bash
for test in web/tests/*.test.mjs; do
  node "$test"
done
```

Expected: PASS. If a browser integration test requires the running service, report it separately rather than treating it as a source-test failure.

- [ ] **Step 3: Build the frontend and record the generated entry**

Run:

```bash
npm run build --prefix web
rg -n 'assets/index-[A-Za-z0-9_-]+\.js' web/dist/index.html
```

Expected: PASS; `index.html` references an `assets/index-<new-content-hash>.js` entry that differs from the pre-change `index-CYjassRv.js`.

- [ ] **Step 4: Run final static checks**

Run:

```bash
npm run typecheck --prefix web
git diff --check
git status --short
```

Expected: all commands pass; only intended source, test, spec, plan, and pre-existing App sync changes remain.

- [ ] **Step 5: Request an independent code review**

Dispatch a `code-reviewer` agent to inspect the completed diff for behavior regressions, especially persisted-node migration, removed preference setters, and the shared desktop/mobile header path. Resolve every CRITICAL or HIGH finding and rerun the commands above.

- [ ] **Step 6: Commit the verification-aligned final diff if the user requested commits**

```bash
git add web/src/services/nodeRegistry.ts web/src/components/DefaultListView.tsx web/src/components/FileTree.tsx web/src/App.tsx web/tests/node-colors-and-workspace-preferences.test.mjs docs/superpowers/specs/2026-08-21-node-colors-and-project-header-design.md docs/superpowers/plans/2026-08-21-node-colors-project-header-workspace-preferences.md
git commit -m "fix: 统一节点主题色与工作区偏好"
```

# 会话锁定与可拖动浮动会话面板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep an explicitly selected conversation as the current project's message target while browsing that project's content, and make the floating conversation sheet open at the newest message with a real drag-to-promote-or-close handle.

**Architecture:** Reuse the existing per-root bound session key as the canonical lock: a non-empty real key locks an existing session, while `null` means the already-supported client-side “first message creates a new session” intent. `selectedSession` remains only the main-pane presentation state, so file/Git/directory navigation may hide the main chat without changing the lock. Extend the existing `BottomSheet` rather than adding a drawer library; its handle gets pointer-based drag resolution, and its existing content scroller is passed to `SessionViewer` so the viewer's existing latest-message logic works in drawer mode.

**Tech Stack:** React 19, TypeScript 5.5, Vite 5, Node `assert` source/unit contracts, Go backend unchanged.

## Global Constraints

- Do not add a backend empty-session endpoint: “blank new session” is a client-side intent and becomes a real session only when the first message is sent.
- Do not persist the lock in `localStorage`, IndexedDB, or any new storage layer.
- Do not add dependencies or a second drawer component; extend `web/src/components/BottomSheet.tsx`.
- A same-project file, directory, Git, task, preview, or floating-sheet close action must not change the lock.
- Only explicit right-sidebar selection, explicit left-swipe new, project/root transition, deletion of the locked session, and an unavailable locked session may change the lock.
- Project/root transition and deletion reset to the client-side blank-new intent; they must not restore a historical target-project session.
- Handle dragging must use Pointer Events; only the visual handle is draggable; `pointercancel` resolves to half-sheet.
- Releasing near the viewport top promotes the drawer session to the existing main chat view and closes the drawer; it does not create a 100vh sheet.
- Use `export PATH="/home/nnb/.local/share/go/bin:$PATH"` before Go commands.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `web/src/services/sessionLock.ts` | Pure normalization and routing helpers for the canonical per-root lock; no React or browser APIs. |
| `web/src/services/bottomSheetModel.ts` | Pure drag-threshold and release-target decisions, testable without DOM/React. |
| `web/src/components/BottomSheet.tsx` | Own the sheet's pointer lifecycle and expose its actual scrolling content element by ref. |
| `web/src/components/SessionViewer.tsx` | Use the supplied outer scroll container in drawer mode so current stick-to-latest behavior works for the actual scrolling element. |
| `web/src/App.tsx` | Make `boundSessionByRootRef` the only send/list/drawer lock, preserve it through same-root navigation, reset it on root transition/deletion, and wire the drawer scroll ref. |
| `web/tests/session-lock.test.mjs` | Pure session-lock behavior tests plus source contracts for `App.tsx` wiring. |
| `web/tests/bottom-sheet-model.test.mjs` | Pure pointer-release threshold behavior tests plus source contracts for sheet/viewer integration. |

## Task 1: Add a pure session-lock model and its failing unit tests

**Files:**
- Create: `web/src/services/sessionLock.ts`
- Create: `web/tests/session-lock.test.mjs`

**Interfaces:**
- Produces `normalizeSessionLockKey(value: unknown): string | null`.
- Produces `resolveLockedSessionKey(value: unknown): string | undefined`.
- Produces `shouldResetSessionLockForRootChange(currentRoot: string | null | undefined, targetRoot: string | null | undefined): boolean`.
- `App.tsx` will use `resolveLockedSessionKey` for sending and queue operations, and `shouldResetSessionLockForRootChange` before cross-root navigation.

- [ ] **Step 1: Write the failing pure-model test**

Create `web/tests/session-lock.test.mjs`. Transpile the module like the existing `web/tests/session-list-merge.test.mjs`; then assert the complete lock contract:

```js
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const sourcePath = path.resolve("src/services/sessionLock.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const {
  normalizeSessionLockKey,
  resolveLockedSessionKey,
  shouldResetSessionLockForRootChange,
} = sandbox.exports;

assert.equal(normalizeSessionLockKey(" session-a "), "session-a");
assert.equal(normalizeSessionLockKey(""), null);
assert.equal(normalizeSessionLockKey(null), null);
assert.equal(normalizeSessionLockKey("pending-123"), "pending-123");
assert.equal(resolveLockedSessionKey("session-a"), "session-a");
assert.equal(resolveLockedSessionKey("pending-123"), undefined);
assert.equal(resolveLockedSessionKey(null), undefined);
assert.equal(shouldResetSessionLockForRootChange("root-a", "root-a"), false);
assert.equal(shouldResetSessionLockForRootChange("root-a", "root-b"), true);
assert.equal(shouldResetSessionLockForRootChange(null, "root-b"), false);
```

Also read `web/src/App.tsx` as text in this test and add source contracts that will fail until Task 2:

```js
const app = fs.readFileSync(path.resolve("src/App.tsx"), "utf8");
assert.match(app, /resolveLockedSessionKey\(activeBoundSessionKey\)/);
assert.match(app, /selectedKey=\{activeBoundSessionKey \|\| ""\}/);
assert.match(app, /shouldResetSessionLockForRootChange\(/);
```

- [ ] **Step 2: Run the test and verify the expected RED failure**

Run:

```bash
cd web && node tests/session-lock.test.mjs
```

Expected: `ENOENT` for `src/services/sessionLock.ts`, or an assertion failure for the not-yet-wired `App.tsx` contracts.

- [ ] **Step 3: Implement the smallest pure model**

Create `web/src/services/sessionLock.ts` with no React imports:

```ts
export function normalizeSessionLockKey(value: unknown): string | null {
  const key = typeof value === "string" ? value.trim() : "";
  return key || null;
}

export function resolveLockedSessionKey(value: unknown): string | undefined {
  const key = normalizeSessionLockKey(value);
  return key && !key.startsWith("pending-") ? key : undefined;
}

export function shouldResetSessionLockForRootChange(
  currentRoot: string | null | undefined,
  targetRoot: string | null | undefined,
): boolean {
  const current = String(currentRoot || "").trim();
  const target = String(targetRoot || "").trim();
  return !!current && !!target && current !== target;
}
```

Do not model an empty session object or add storage. `null` is deliberately the blank-new-session intent already understood by the existing send flow.

- [ ] **Step 4: Run the pure test and typecheck**

Run:

```bash
cd web && node tests/session-lock.test.mjs
npm run typecheck --prefix web
```

Expected: the pure assertions pass; the three source-wiring assertions still fail until Task 2. This is intentional RED for the integration half of the same test.

- [ ] **Step 5: Commit the model and RED integration contract together**

```bash
git add web/src/services/sessionLock.ts web/tests/session-lock.test.mjs
git commit -m "test: 添加会话锁定模型契约"
```

## Task 2: Make the bound per-root key the canonical session lock

**Files:**
- Modify: `web/src/App.tsx:126 imports; 1300-1320 root-scoped refs; 2614-2654 root setters; 4800-4876 Git navigation; 5067-5194 explicit selection; 5280-5380 deletion; 5891-6459 send path; 6653-6677 explicit new; 6810-7167 file/directory navigation; 10525-10579 derived action-bar state; 13460-13503 session-list props; 13781-13815 drawer actions`
- Modify: `web/tests/session-lock.test.mjs`

**Interfaces:**
- Consumes `normalizeSessionLockKey`, `resolveLockedSessionKey`, and `shouldResetSessionLockForRootChange` from Task 1.
- Produces a single lock representation: `boundSessionByRootRef.current[rootId]`, mirrored as `activeBoundSessionKey` only for the active root.
- `null` in this map is the client-only blank-new intent; a non-pending key is an existing-session send target.

- [ ] **Step 1: Extend the test with source assertions for explicit state transitions**

Append assertions to `web/tests/session-lock.test.mjs` that make the intended integration unambiguous:

```js
assert.match(
  app,
  /const lockedSessionKey = resolveLockedSessionKey\(activeBoundSessionKey\);/,
  "ActionBar/send routing must derive from the canonical active bound lock",
);
assert.match(
  app,
  /selectedKey=\{activeBoundSessionKey \|\| ""\}/,
  "single-project list must highlight the lock even when the main view is a file",
);
assert.match(
  app,
  /selectedKey=\{activeBoundSessionKey \|\| ""\}[\s\S]*selectedRootId=\{currentRootId \|\| ""\}/,
  "multi-project list must highlight the active root lock",
);
assert.match(
  app,
  /setBoundSessionForRoot\(targetRoot, key\);/,
  "explicit sidebar selection must update the canonical lock",
);
assert.match(
  app,
  /if \(shouldResetSessionLockForRootChange\([\s\S]*?setBoundSessionForRoot\([^\n]+, null\);[\s\S]*?setDrawerSessionForRoot\([^\n]+, null\);/,
  "cross-project navigation must reset both source and target lock state",
);
```

Add negative contracts that protect the regression:

```js
assert.doesNotMatch(
  app,
  /setMainViewPreferenceForRoot\(String\(root\), "file"\)[\s\S]{0,160}setBoundSessionForRoot\(String\(root\), null\)/,
  "same-project file navigation must not clear the bound lock",
);
assert.doesNotMatch(
  app,
  /setMainViewPreferenceForRoot\(root, "directory"\)[\s\S]{0,160}setBoundSessionForRoot\(root, null\)/,
  "same-project directory navigation must not clear the bound lock",
);
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
cd web && node tests/session-lock.test.mjs
```

Expected: source-contract assertion failure because `App.tsx` still resolves send/list state from `selectedSession` and does not reset only on cross-root navigation.

- [ ] **Step 3: Add canonical-lock helpers inside `App` and import the pure model**

Add the Task 1 import near other service imports:

```ts
import {
  normalizeSessionLockKey,
  resolveLockedSessionKey,
  shouldResetSessionLockForRootChange,
} from "./services/sessionLock";
```

Near `setBoundSessionForRoot`, add focused local helpers rather than introducing a provider or new storage:

```ts
const resetSessionLockForRoot = useCallback((rootID: string | null | undefined) => {
  const root = String(rootID || "").trim();
  if (!root) return;
  setBoundSessionForRoot(root, null);
  setDrawerSessionForRoot(root, null);
  selectedSessionByRootRef.current[root] = null;
  setDrawerOpenForRoot(root, false);
  if (currentRootIdRef.current === root) {
    selectedSessionRef.current = null;
    setSelectedSession(null);
    setSelectedSessionLoading(false);
    interactionModeRef.current = "main";
    setInteractionMode("main");
  }
}, [setBoundSessionForRoot, setDrawerOpenForRoot, setDrawerSessionForRoot]);

const resetLocksForRootTransition = useCallback((targetRoot: string | null | undefined) => {
  const sourceRoot = currentRootIdRef.current;
  if (!shouldResetSessionLockForRootChange(sourceRoot, targetRoot)) return;
  resetSessionLockForRoot(sourceRoot);
  resetSessionLockForRoot(targetRoot);
}, [resetSessionLockForRoot]);
```

Use `normalizeSessionLockKey` whenever comparing map keys, so whitespace cannot create a false selected state. Do not call these helpers for same-root content navigation.

- [ ] **Step 4: Route every explicit lock transition through the canonical key**

Make the following exact semantic changes:

1. In `handleSelectSession`, immediately after resolving `targetRoot`/`key`, call `setBoundSessionForRoot(targetRoot, key)` and `setDrawerSessionForRoot(targetRoot, toSessionItem(targetRoot, session))`. Keep assigning `selectedSessionByRootRef.current[targetRoot] = key` only as legacy restoration metadata during this task, but derive UI/send behavior from the bound key.
2. In `handleNewSession`, retain the existing clear-to-new behavior but replace duplicated individual resets with `resetSessionLockForRoot(rootID)`. This must leave the active root in the client-only blank intent, not create a server session or list item.
3. In `handleDeleteSession`, when `deletedKeys` contains `boundSessionByRootRef.current[rootID]`, call `resetSessionLockForRoot(rootID)` after caches/list items are removed. This covers deletion while the main pane is a file as well as deletion while chat is visible.
4. In every path that can enter another root (`actionHandlers.open`, `open_dir` before `setCurrentRootId`, `openGitDiff`, `openGitCommitDiff`, and related Git diff), call `resetLocksForRootTransition(targetRoot)` before changing `currentRootId`. For an actual root switch, do not call `tryShowBoundSessionForRoot`; route to the directory/content view and leave the target at blank intent. For same-root calls, the helper is a no-op.
5. Remove only the `setSelectedSession(null)`/loading clears needed to show workspace content **after** retaining the bound lock. Leave `setSelectedSession(null)` in same-root file/directory/Git navigation; it correctly hides the main chat pane. Do not clear `boundSessionByRootRef`, `drawerSessionByRootRef`, or `selectedSessionByRootRef` there.
6. In the root-change synchronization effect, do not resurrect a bound/drawer session for a root entered through a user project switch. Since the transition helper has reset the destination maps, the existing effect safely mirrors `null`; verify `tryShowBoundSessionForRoot` is skipped for this transition.

- [ ] **Step 5: Make all user-visible selection and sending derive from the lock**

At the existing derived-state block near `selectedRoot`, introduce:

```ts
const lockedSessionKey = resolveLockedSessionKey(activeBoundSessionKey);
const lockedSessionSnapshot = lockedSessionKey && currentRootId
  ? getSessionSnapshot(
      currentRootId,
      drawerSessionByRootRef.current[currentRootId] ||
        sessionCacheRef.current[rootSessionKey(currentRootId, lockedSessionKey)] ||
        null,
    )
  : null;
```

Then apply these rules:

- Pass `activeBoundSessionKey || ""` and `currentRootId || ""` to both `SessionList` variants, so a file-view workspace still leaves the explicitly locked row gray-selected.
- Make `actionBarSession` use `lockedSessionSnapshot` (or the existing drawer/cache fallback for the same key), not the currently rendered `selectedSession` as the authority.
- In `handleSendMessage`, derive `sendSessionKey` from `resolveLockedSessionKey(boundSessionByRootRef.current[activeRoot])`. A `null` lock intentionally produces `undefined`, preserving the existing first-message new-session path. Never select an unrelated historical `selectedSession` merely because it remains in memory.
- Apply the same resolved key rule to queued-message remove/update/send helpers.
- Preserve the existing `pending-` request behavior: it remains a local in-flight transport key and must not be passed as a server session key.
- Keep URL session values only for an explicit right-list selection/main chat view; browsing a file can continue to write `session: ""` without affecting the bound lock.

- [ ] **Step 6: Run GREEN checks**

Run:

```bash
cd web && node tests/session-lock.test.mjs
npm run typecheck --prefix web
```

Expected: all pure behavior and source contracts pass; TypeScript has no errors.

- [ ] **Step 7: Manual browser acceptance for lock semantics**

With the app running, verify in this order:

1. Select an existing session in the right list, open several files/directories/Git views in the same project, and confirm its right-list row remains gray-selected.
2. Send from each of those views and confirm the message appends to the originally selected session.
3. Left-swipe to new, open a file in the same project, then send once; confirm exactly one new session is created from that message.
4. Switch project; confirm no previous target-project conversation is selected, then send once and confirm a new session is created there.
5. Delete the locked session while viewing a file; confirm no old row remains selected and the next send creates one new session.

- [ ] **Step 8: Commit the lock integration**

```bash
git add web/src/App.tsx web/tests/session-lock.test.mjs
git commit -m "fix: 锁定项目会话并避免浏览内容时丢失"
```

## Task 3: Add a pure bottom-sheet drag model and its tests

**Files:**
- Create: `web/src/services/bottomSheetModel.ts`
- Create: `web/tests/bottom-sheet-model.test.mjs`

**Interfaces:**
- Produces `BOTTOM_SHEET_DRAG_START_PX = 8`.
- Produces `BOTTOM_SHEET_EDGE_RATIO = 0.2`.
- Produces `type BottomSheetRelease = "half" | "close" | "expand"`.
- Produces `resolveBottomSheetRelease(input): BottomSheetRelease`.
- `BottomSheet` will map `expand` to the existing `onExpand()` callback, `close` to `onClose()`, and `half` to no external state change.

- [ ] **Step 1: Write failing tests for exact threshold behavior**

Create `web/tests/bottom-sheet-model.test.mjs`, using the same TypeScript-transpile/VM pattern as Task 1. Assert concrete values to remove threshold ambiguity:

```js
assert.equal(BOTTOM_SHEET_DRAG_START_PX, 8);
assert.equal(BOTTOM_SHEET_EDGE_RATIO, 0.2);

assert.equal(
  resolveBottomSheetRelease({ clientY: 150, viewportHeight: 1000, dragged: true }),
  "expand",
);
assert.equal(
  resolveBottomSheetRelease({ clientY: 850, viewportHeight: 1000, dragged: true }),
  "close",
);
assert.equal(
  resolveBottomSheetRelease({ clientY: 500, viewportHeight: 1000, dragged: true }),
  "half",
);
assert.equal(
  resolveBottomSheetRelease({ clientY: 100, viewportHeight: 1000, dragged: false }),
  "half",
);
```

Read `components/BottomSheet.tsx` as text and add contracts that must initially fail:

```js
assert.match(sheet, /onPointerDown=\{handlePointerDown\}/);
assert.match(sheet, /onPointerMove=\{handlePointerMove\}/);
assert.match(sheet, /onPointerUp=\{handlePointerUp\}/);
assert.match(sheet, /onPointerCancel=\{handlePointerCancel\}/);
assert.doesNotMatch(sheet, /onClick=\{onExpand\}/);
assert.match(sheet, /contentRef\?: React\.RefObject<HTMLDivElement \| null>/);
```

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
cd web && node tests/bottom-sheet-model.test.mjs
```

Expected: missing `bottomSheetModel.ts` or source contract failures.

- [ ] **Step 3: Implement the pure release model**

Create `web/src/services/bottomSheetModel.ts`:

```ts
export const BOTTOM_SHEET_DRAG_START_PX = 8;
export const BOTTOM_SHEET_EDGE_RATIO = 0.2;

export type BottomSheetRelease = "half" | "close" | "expand";

export function resolveBottomSheetRelease({
  clientY,
  viewportHeight,
  dragged,
}: {
  clientY: number;
  viewportHeight: number;
  dragged: boolean;
}): BottomSheetRelease {
  if (!dragged || viewportHeight <= 0) return "half";
  if (clientY <= viewportHeight * BOTTOM_SHEET_EDGE_RATIO) return "expand";
  if (clientY >= viewportHeight * (1 - BOTTOM_SHEET_EDGE_RATIO)) return "close";
  return "half";
}
```

- [ ] **Step 4: Run the pure model test and retain RED component assertions**

Run:

```bash
cd web && node tests/bottom-sheet-model.test.mjs
```

Expected: pure threshold assertions pass; `BottomSheet` source contracts remain red until Task 4.

- [ ] **Step 5: Commit the model and RED component contract**

```bash
git add web/src/services/bottomSheetModel.ts web/tests/bottom-sheet-model.test.mjs
git commit -m "test: 添加浮动会话面板拖拽契约"
```

## Task 4: Implement pointer dragging and connect drawer scroll-to-latest

**Files:**
- Modify: `web/src/components/BottomSheet.tsx:1-120`
- Modify: `web/src/components/SessionViewer.tsx:53-96 props; 1039-1084 refs/helper; 1269-1355 scroll effects; 2414-2417 scroll container`
- Modify: `web/src/App.tsx:1287-1320 refs; 13804-13871 BottomSheet/SessionViewer wiring`
- Modify: `web/tests/bottom-sheet-model.test.mjs`

**Interfaces:**
- Consumes Task 3 `BOTTOM_SHEET_DRAG_START_PX` and `resolveBottomSheetRelease`.
- `BottomSheetProps` gains `contentRef?: React.RefObject<HTMLDivElement | null>`; it points to the existing `overflow: auto` content wrapper.
- `SessionViewerProps` gains `scrollContainerRef?: React.RefObject<HTMLDivElement | null>`; when `interactionMode === "drawer"`, it uses this external scroll element for all current bottom-stick calculations and scroll listeners.
- `App` owns one stable `drawerScrollRef` and passes it to both components.

- [ ] **Step 1: Extend failing source contracts for the scroll handoff**

Append to `web/tests/bottom-sheet-model.test.mjs`:

```js
const viewer = fs.readFileSync(path.resolve("src/components/SessionViewer.tsx"), "utf8");
const app = fs.readFileSync(path.resolve("src/App.tsx"), "utf8");

assert.match(
  viewer,
  /scrollContainerRef\?: React\.RefObject<HTMLDivElement \| null>/,
  "drawer-mode SessionViewer must receive the real sheet scroller",
);
assert.match(
  viewer,
  /const activeScrollRef = interactionMode === "drawer" \? scrollContainerRef : scrollRef;/,
  "stick-to-bottom must target the outer drawer scroller in drawer mode",
);
assert.match(app, /const drawerScrollRef = useRef<HTMLDivElement \| null>\(null\);/);
assert.match(app, /<BottomSheet[\s\S]*contentRef=\{drawerScrollRef\}/);
assert.match(app, /<SessionViewer[\s\S]*scrollContainerRef=\{drawerScrollRef\}/);
```

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
cd web && node tests/bottom-sheet-model.test.mjs
```

Expected: source-contract failures for pointer handlers and the external scrolling ref.

- [ ] **Step 3: Extend `BottomSheet` without changing its public meaning**

Update `BottomSheetProps` and imports:

```tsx
import React, { useEffect, useRef, useState } from "react";
import {
  BOTTOM_SHEET_DRAG_START_PX,
  resolveBottomSheetRelease,
} from "../services/bottomSheetModel";

// in BottomSheetProps
contentRef?: React.RefObject<HTMLDivElement | null>;
```

Inside `BottomSheet`, add only local gesture state/refs:

```tsx
const startYRef = useRef(0);
const lastYRef = useRef(0);
const pointerIdRef = useRef<number | null>(null);
const [dragOffsetY, setDragOffsetY] = useState(0);
const [isDragging, setIsDragging] = useState(false);
```

Implement the handlers on the handle only:

```tsx
const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
  pointerIdRef.current = event.pointerId;
  startYRef.current = event.clientY;
  lastYRef.current = event.clientY;
  setIsDragging(false);
  event.currentTarget.setPointerCapture(event.pointerId);
};

const handlePointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
  if (pointerIdRef.current !== event.pointerId) return;
  const deltaY = event.clientY - startYRef.current;
  if (!isDragging && Math.abs(deltaY) >= BOTTOM_SHEET_DRAG_START_PX) {
    setIsDragging(true);
  }
  if (Math.abs(deltaY) >= BOTTOM_SHEET_DRAG_START_PX) {
    lastYRef.current = event.clientY;
    setDragOffsetY(deltaY);
  }
};

const finishPointer = (
  event: React.PointerEvent<HTMLDivElement>,
  cancelled: boolean,
) => {
  if (pointerIdRef.current !== event.pointerId) return;
  if (event.currentTarget.hasPointerCapture(event.pointerId)) {
    event.currentTarget.releasePointerCapture(event.pointerId);
  }
  const release = cancelled
    ? "half"
    : resolveBottomSheetRelease({
        clientY: lastYRef.current,
        viewportHeight: window.innerHeight,
        dragged: isDragging,
      });
  pointerIdRef.current = null;
  setDragOffsetY(0);
  setIsDragging(false);
  if (release === "expand") onExpand?.();
  if (release === "close") onClose();
};
```

Bind `onPointerDown`, `onPointerMove`, `onPointerUp={(event) => finishPointer(event, false)}`, and `onPointerCancel={(event) => finishPointer(event, true)}` to the 8px handle. Delete `onClick={onExpand}`. Keep the existing 8px visual-only handle and do not attach handlers to the panel/content/footer.

Apply `translateY(${dragOffsetY}px)` while dragging and use the existing open/close transform otherwise. Disable transition only while `isDragging`; restore it after release. Clamp visual movement to the viewport bounds only for rendering, but use the actual release `clientY` for model resolution.

Attach the supplied ref to the **existing** outer scroll wrapper:

```tsx
<div
  ref={contentRef}
  style={{ flex: 1, overflow: "auto", WebkitOverflowScrolling: "touch", minHeight: 0 }}
>
```

Do not change overlay click semantics: it remains an ordinary close and therefore must not mutate the lock.

- [ ] **Step 4: Make `SessionViewer` use the actual drawer scroll element**

Extend `SessionViewerProps`:

```ts
scrollContainerRef?: React.RefObject<HTMLDivElement | null>;
```

Destructure it and define a single active ref immediately after the existing `scrollRef`:

```ts
const activeScrollRef = interactionMode === "drawer" ? scrollContainerRef : scrollRef;
```

Replace only scroll-container accesses in `stickSessionToBottom`, `readCurrentUserMessageIndex`, `scrollToUserMessageSummary`, target-sequence scroll, viewport-sticky effect, and user-scroll listener from `scrollRef.current` to `activeScrollRef?.current`. Preserve the visible inner message wrapper as `overflowY: "visible"` in drawer mode; its parent is now the active scroller.

Update the current effects so drawer mode is no longer early-returned merely because `useInnerScrollContainer` is false. Their guards must require `activeScrollRef?.current`, not `useInnerScrollContainer`. Keep the main-view-only UI behavior unchanged: header and jump-to-latest button remain hidden for `interactionMode="drawer"`.

The content-change effect must run after the outer ref is mounted; schedule a second `requestAnimationFrame` retry when `activeScrollRef.current` is unavailable on the first effect pass. This is a retry only; it must not block opening the sheet.

- [ ] **Step 5: Wire one stable drawer scroller through `App`**

Near existing per-root refs, add:

```tsx
const drawerScrollRef = useRef<HTMLDivElement | null>(null);
```

At the existing drawer JSX (`App.tsx` around `13805`), pass the same ref to both participants:

```tsx
<BottomSheet
  isOpen={isDrawerOpen}
  contentRef={drawerScrollRef}
  onClose={...}
  onExpand={...}
>
  <SessionViewer
    // existing props
    interactionMode="drawer"
    scrollContainerRef={drawerScrollRef}
  />
</BottomSheet>
```

Do not change `onExpand`: Task 3's `expand` intentionally calls the existing callback, which selects the drawer session in the main viewer and closes the sheet. Keep `onClose` as a drawer-close-only action; it must preserve `boundSessionByRootRef`.

- [ ] **Step 6: Run GREEN checks**

Run:

```bash
cd web && node tests/bottom-sheet-model.test.mjs
npm run typecheck --prefix web
```

Expected: all pure threshold and source wiring contracts pass; TypeScript has no errors.

- [ ] **Step 7: Manual browser acceptance for drawer behavior**

Select a session, open a same-project file, click the blue up-arrow ring, and verify:

1. The floating panel opens at its 75vh half-sheet size with the latest message visible.
2. A click/tap on the handle does not promote or close the panel.
3. Dragging the handle into the top 20% of the viewport promotes the selected conversation to the middle main chat view and closes the panel.
4. Reopen the panel, drag into the bottom 20%, and verify it closes while the right-list selection and send target remain unchanged.
5. Start a drag then cancel it (e.g., pointer leaves/OS interruption); verify it returns to normal half-sheet position.
6. Scroll upward in the floating conversation; incoming content must not forcibly pull the reader back down unless the existing sticky-at-bottom condition is true.

- [ ] **Step 8: Run full regression checks and commit**

Run:

```bash
cd web && node tests/session-lock.test.mjs && node tests/bottom-sheet-model.test.mjs
npm run typecheck --prefix web
cd .. && export PATH="/home/nnb/.local/share/go/bin:$PATH" && make test
```

Expected: both new Node tests, TypeScript checking, and Go suite pass.

Commit:

```bash
git add web/src/components/BottomSheet.tsx web/src/components/SessionViewer.tsx web/src/App.tsx web/tests/bottom-sheet-model.test.mjs
git commit -m "feat: 支持可拖动会话浮层与最新消息定位"
```

## Final verification and review gate

- [ ] **Step 1: Inspect the full branch diff for scope control**

Run:

```bash
git diff main...HEAD -- web/src/App.tsx web/src/components/BottomSheet.tsx web/src/components/SessionViewer.tsx web/src/services/sessionLock.ts web/src/services/bottomSheetModel.ts web/tests
```

Verify no backend, storage, dependency, or unrelated UI changes were introduced.

- [ ] **Step 2: Run final static and test checks**

Run:

```bash
cd web && node tests/session-lock.test.mjs && node tests/bottom-sheet-model.test.mjs
npm run typecheck --prefix web
cd .. && export PATH="/home/nnb/.local/share/go/bin:$PATH" && make test
git diff --check
git status --short --branch
```

Expected: all commands pass and there are no uncommitted files.

- [ ] **Step 3: Request an independent code review**

Ask a fresh reviewer to inspect the complete diff specifically for:

- any same-root call path that clears the bound session lock;
- any project transition path that restores a prior target-project lock;
- pending-session (`pending-*`) send behavior and deletion failure cleanup;
- pointer capture/release and `pointercancel` safety;
- drawer scrolling targeting the actual `BottomSheet` scroller.

Resolve all CRITICAL/HIGH findings, rerun Step 2, then commit any review fixes with a Conventional Commit message.

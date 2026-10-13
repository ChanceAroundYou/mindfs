import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("project lifecycle delegates worktree overlays while retaining state and actions in the hook", () => {
  const hook = read("../src/app/useProjectLifecycle.tsx");
  const overlays = read("../src/components/file/WorktreeLifecycleOverlays.tsx");

  assert.match(hook, /<WorktreeLifecycleOverlays[\s\S]*?mode="create"/);
  assert.match(hook, /<WorktreeLifecycleOverlays[\s\S]*?mode="switch"/);
  assert.match(hook, /const handleCreateRootSubmit = useCallback/);
  assert.match(hook, /const handleSwitchWorktree = useCallback/);
  assert.match(overlays, /event\.key === "Enter"[\s\S]*?handleCreateRootSubmit\(\)/);
  assert.match(overlays, /event\.key === "Escape"[\s\S]*?handleCreateRootCancel\(\)/);
  assert.match(overlays, /worktreeSwitchLoading[\s\S]*?worktreeSwitchError[\s\S]*?worktreeSwitchItems\.length === 0/);
  assert.match(overlays, /active \? t\("worktree\.current"\) : managed \? t\("worktree\.switch"\) : t\("worktree\.join"\)/);
  assert.match(overlays, /mode === "create" \? worktreeCreateOverlay : worktreeSwitchOverlay/);
  assert.doesNotMatch(overlays, /from ["'][^"']*\/app\//);
});

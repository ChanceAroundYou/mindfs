import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const fileTree = readFileSync(new URL("../src/components/FileTree.tsx", import.meta.url), "utf8");
const fileService = readFileSync(new URL("../src/services/file.ts", import.meta.url), "utf8");

assert.match(
  fileTree,
  /onRefresh\?: \(tab: ProjectTreeTab\) => void \| Promise<void>/,
  "FileTree refresh should identify the active project tab",
);
assert.match(
  fileTree,
  /\(\) => onRefresh\?\.\(projectTreeTab\)/,
  "FileTree should refresh the tab that is active when the button is clicked",
);
assert.match(
  fileTree,
  /flexShrink: 0, gap: 0, overflow: "visible"[\s\S]*?maxWidth: "calc\(100% - 56px\)", marginRight: "6px"/,
  "refresh should keep a 6px tab gap while reclaiming refresh control width",
);
assert.match(
  fileTree,
  /data-onboarding="sidebar-refresh"[\s\S]*?width: "22px"[\s\S]*?justifyContent: "flex-end"/,
  "refresh hit area should shrink without moving its right edge",
);
assert.match(
  fileTree,
  /data-refresh-visual[\s\S]*?width: "18px"[\s\S]*?background: treePressed \|\| treeRefreshing[\s\S]*?justifyContent: "center"/,
  "refresh feedback background should center tightly around the icon",
);

assert.match(
  app,
  /case "files":[\s\S]*?const treeBase = scopeKey\(getNodeIdForRoot\(root\) \?\? "", root\);[\s\S]*?const targets = new Set<string>\(\[selDir\]\);[\s\S]*?for \(const key of Object\.keys\(entriesByPathRef\.current\)\)[\s\S]*?await Promise\.all\(\s*\[\.\.\.targets\]\.map\(\(dir\) => refreshTreeDir\(root, dir, dir === selDir\)\),\s*\)/,
  "files refresh must re-pull every loaded directory of the current root, not only the selected one",
);
assert.match(
  app,
  /if \(fileRef\.current\?\.path\) \{[\s\S]*?void refreshCurrentFileContent\(root, fileRef\.current\.path\);\s*\}/,
  "files refresh should force a re-read of the currently open file",
);
assert.match(
  app,
  /refreshTreeDir = useCallback\(\s*async \(rootID: string, dirPath: string, syncMain: boolean\) => \{\s*const cacheKey = treeCacheKey\(rootID, dirPath\);[\s\S]*?treeFetchSeqRef\.current\[cacheKey\] = \(treeFetchSeqRef\.current\[cacheKey\] \|\| 0\) \+ 1;[\s\S]*?if \(treeFetchSeqRef\.current\[cacheKey\] !== seq\) \{[\s\S]*?invalidTreeCacheKeysRef\.current\.add\(cacheKey\);[\s\S]*?setMainDirectoryError\(display\);/,
  "refreshTreeDir should drop stale concurrent responses, distrust cache on failure, and surface errors for the viewed directory",
);
assert.match(
  app,
  /appURL\("\/api\/tree", new URLSearchParams\(\{ root: rootID, dir: dirPath \}\), getNodeIdForRoot\(rootID\)\)/,
  "directory refresh must route through the root's own node so it works on non-local nodes",
);
assert.match(
  app,
  /case "git":[\s\S]*?refreshGitStatus\(root\)[\s\S]*?refreshGitHistory\(root, \{ waitForIncremental: true \}\)/,
  "git refresh should reload status and await an incremental history probe",
);
assert.match(
  app,
  /case "worktrees":[\s\S]*?loadProjectTreeWorktrees\(root\)[\s\S]*?expandedWorktreeByRoot\[root\][\s\S]*?loadProjectTreeWorktreeStatus\(expandedPath\)/,
  "worktree refresh should reload the list and only the expanded worktree status",
);
assert.match(
  app,
  /fetchGitHistory\(rootID, \{ afterCommit: newest(?:, nodeId: [^}]+)? \}\)[\s\S]*?options\?\.waitForIncremental[\s\S]*?return refreshAfterNewest\(\)/,
  "manual history refresh should await the existing after_commit probe",
);
assert.match(
  app,
  /data-onboarding="task-refresh"[\s\S]*?width: "22px"[\s\S]*?data-task-refresh-visual[\s\S]*?width: "18px"/,
  "task refresh should use a compact hit area and centered feedback background",
);
assert.match(
  app,
  /data-onboarding="task-create"[\s\S]*?width: "28px"[\s\S]*?background: taskCreateTemplateMenuOpen[\s\S]*?justifyContent: "center"/,
  "task create should retain its normal button size",
);
assert.match(
  app,
  /case "related":[\s\S]*?refreshProjectTreeRelatedFiles\(\)[\s\S]*?refreshGitStatus\(root\)/,
  "related refresh should reload relationships and current file statistics",
);

assert.match(
  fileService,
  /export function invalidateFileCache[\s\S]*?rawFileFailures\.delete\(buildRawFileFailureKey\(rootId, path\)\)/,
  "file-change invalidation should clear the matching raw 404 cache entry",
);
assert.match(
  fileService,
  /for \(const key of Array\.from\(rawFileBlobCache\.keys\(\)\)\) \{\s*if \(key\.startsWith\(`\$\{rootId\}:\$\{path\}:`\)\) rawFileBlobCache\.delete\(key\);\s*\}/,
  "file-change invalidation should also drop raw blob cache entries for the path (no-TTL image cache)",
);
assert.match(
  fileService,
  /export function clearFileCacheForRoot[\s\S]*?for \(const key of rawFileFailures\.keys\(\)\)[\s\S]*?rawFileFailures\.delete\(key\)/,
  "root cache clearing should remove raw 404 entries",
);
assert.match(
  fileService,
  /if \(failedAt !== undefined\)[\s\S]*?Date\.now\(\) - failedAt < RAW_FILE_FAILURE_TTL_MS[\s\S]*?rawFileFailures\.delete\(cacheKey\)/,
  "expired raw 404 entries should be removed instead of accumulating",
);

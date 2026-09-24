import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const fileTree = readFileSync(new URL("../src/components/FileTree.tsx", import.meta.url), "utf8");
const viewer = readFileSync(new URL("../src/components/SessionViewer.tsx", import.meta.url), "utf8");
// 2026-09 App.tsx 拆分：相关文件页签的渲染搬到 components/RootRelatedContentView.tsx，契约随文件走。
const rootRelatedView = readFileSync(new URL("../src/components/RootRelatedContentView.tsx", import.meta.url), "utf8");
// 2026-09 App.tsx 拆分：worktree 页签的渲染搬到 components/RootWorktreeContentView.tsx，契约随文件走。
const rootWorktreeView = readFileSync(new URL("../src/components/RootWorktreeContentView.tsx", import.meta.url), "utf8");
const rootGitView = readFileSync(new URL("../src/components/RootGitContentView.tsx", import.meta.url), "utf8");
const fileService = readFileSync(new URL("../src/services/file.ts", import.meta.url), "utf8");
const sessionSvc = readFileSync(new URL("../src/services/session.ts", import.meta.url), "utf8");
const claudeSession = readFileSync(new URL("../../server/internal/agent/claude/session.go", import.meta.url), "utf8");

// 3951aa4 — task→non-task relatedFiles: preserveTaskSelection + showAllFiles removal
assert.match(app, /handleSelectSession[\s\S]*?preserveTaskSelection/, "3951aa4 App should carry preserveTaskSelection");
assert.match(app, /if \(!options\?\.preserveTaskSelection\)[\s\S]*?setSelectedKanbanTaskId\(""\);/, "3951aa4 App should clear kanban task unless preserve");
assert.match(app, /onExpand[\s\S]*?handleSelectSession\(currentSession, \{\s*preserveTaskSelection:/, "3951aa4 App drawer onExpand should pass preserveTaskSelection");
assert.match(viewer, /const displayFiles = relatedFiles;/, "3951aa4 SessionViewer should show all related files without slicing");
assert.doesNotMatch(viewer, /showAllFiles/, "3951aa4 SessionViewer should not have showAllFiles toggle");
assert.doesNotMatch(viewer, /hasMoreFiles/, "3951aa4 SessionViewer should not have hasMoreFiles");
assert.doesNotMatch(viewer, /session\.more/, "3951aa4 SessionViewer should not reference session.more");
assert.doesNotMatch(viewer, /session\.less/, "3951aa4 SessionViewer should not reference session.less");

// 466b21e — newProjectMetaLocation FileTree UI + App upload agent_path
assert.match(fileTree, /fetchNewProjectMetaLocationPreference/, "466b21e FileTree should load newProjectMetaLocation");
assert.match(fileTree, /updateNewProjectMetaLocationPreference/, "466b21e FileTree should persist newProjectMetaLocation");
assert.match(fileTree, /newProjectMetaLocation/, "466b21e FileTree should have newProjectMetaLocation state");
assert.match(fileTree, /--mindfs-file-menu-width/, "466b21e FileTree menu should use mindfs-file-menu-width var");
assert.match(fileTree, /fileTree\.newProjectMetaLocation/, "466b21e FileTree should render newProjectMetaLocation button");
assert.match(app, /file\.agent_path \|\| file\.path/, "466b21e App should use agent_path fallback when building attachment tokens");

// c0a4398 — related-file git-diff line stats hook wiring
assert.match(app, /useRelatedFileStats/, "c0a4398 App should wire useRelatedFileStats");
assert.match(app, /gitStatsRefreshKey/, "c0a4398 App should compute gitStatsRefreshKey");
assert.match(rootRelatedView, /selectedRelatedFileStatsByKey\[relatedFileStatKey\(file\)\]/, "c0a4398 related-files view should resolve stats by relatedFileStatKey");
assert.match(viewer, /useRelatedFileStats/, "c0a4398 SessionViewer should wire useRelatedFileStats");
assert.match(viewer, /relatedFileStatsByKey\[relatedFileStatKey\(file\)\]/, "c0a4398 SessionViewer should resolve stats by relatedFileStatKey");
const hook = readFileSync(new URL("../src/hooks/useRelatedFileStats.ts", import.meta.url), "utf8");
assert.match(hook, /export function useRelatedFileStats/, "c0a4398 hook should exist");

// 5941a36 — claude whitelist removal: ListModels via claudeModelInfo without effort whitelist
assert.match(claudeSession, /for _, model := range supported \{\s*if isHiddenClaudeModel/, "5941a36 ListModels should filter hidden then delegate to claudeModelInfo");
assert.match(claudeSession, /models = append\(models, claudeModelInfo\(model\)\)/, "5941a36 ListModels should use claudeModelInfo for all models");
assert.match(claudeSession, /SupportEffort: true/, "5941a36 claudeModelInfo should expose all effort levels");
assert.doesNotMatch(claudeSession, /claudeModelSupportsEffortAt/, "5941a36 should not reference whitelist EffortAt");

// d36cc53 — frontend reconnect event_seq re-anchor + replaySnapshot branch
assert.match(app, /cachedBeforeSync/, "d36cc53 App restore should capture cachedBeforeSync");
assert.match(app, /getEventCursor/, "d36cc53 App restore should read resumeCursor via getEventCursor");
assert.match(app, /localTransient/, "d36cc53 App restore should keep local seq==0 exchanges");
assert.match(app, /replaySnapshot === true/, "d36cc53 App should branch on replaySnapshot when coalescing userShell");
assert.match(sessionSvc, /eventCursors/, "d36cc53 session.ts should track eventCursors");
assert.match(sessionSvc, /getEventCursor/, "d36cc53 session.ts should expose getEventCursor");

// 8e7d857 — kanban/session-list cache loading (keep local nodeId/scopedRootKey + context_window)
assert.match(app, /getCachedSessionList/, "8e7d857 App should prefetch getCachedSessionList");
assert.match(app, /saveCachedSessionList/, "8e7d857 App should persist saveCachedSessionList");
assert.match(app, /getCachedMultiRootSessionList/, "8e7d857 App should prefetch multi-root cache");
assert.match(app, /saveCachedMultiRootSessionList/, "8e7d857 App should persist multi-root cache");
assert.match(app, /Promise\.all\(\[[\s\S]*?fetchTaskDetails\(targetRoot,[\s\S]*?(?:getNodeIdForRoot|resolveNodeId)\(targetRoot\)\)/, "8e7d857 loadKanbanTasks should fetch in parallel with nodeId");
assert.match(app, /const shouldReplace = options\?\.replace \|\|/, "8e7d857 App should share shouldReplace gate");
assert.match(app, /void saveCachedSessionList\(rootID, payload(?:,\s*[^)]+)?\);/, "8e7d857 App should save session-list after replace with context_window preserved");

// single assertion for fileService raw 404 + blob dedup sanity (from 33c190/466b21e overlap covers 633c190 zone too)
assert.match(fileService, /rawFileFailures/, "file.ts raw 404 cache should exist");
assert.match(fileService, /rawFileBlobCache/, "file.ts blob dedup cache should exist");

// 2026-09 App.tsx 拆分守卫：项目树三个页签的渲染必须留在各自的视图组件里。
// 这三条 renderRoot* 在 App 侧只应是「取好数据 → 交给组件」的转发，
// 一旦有人把 JSX 抄回 App，这里会立刻失败（此前没有测试守这条）。
assert.match(rootWorktreeView, /is_git_repo !== true/, "worktree 视图应保留非 git 根的早退守卫");
assert.match(rootWorktreeView, /<GitStatusPanel/, "worktree 视图应渲染每个 worktree 的 git 状态面板");
assert.match(rootGitView, /<GitHistoryPanel/, "git 视图应渲染历史面板");
assert.match(rootRelatedView, /relatedFileStatKey/, "相关文件视图应按 relatedFileStatKey 取统计");
for (const [name, body] of [["git", app], ["worktree", app], ["related", app]]) {
  assert.doesNotMatch(
    body,
    new RegExp(`const renderRoot${name[0].toUpperCase() + name.slice(1)}Content = \\(root: string\\): React\\.ReactNode => \\{`),
    `renderRoot${name[0].toUpperCase() + name.slice(1)}Content 不应再在 App 里内联实现（应转发给视图组件）`,
  );
}

console.log("upstream restore contracts OK");

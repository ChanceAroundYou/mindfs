import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const uploadSrc = readFileSync(new URL("../src/services/upload.ts", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const streamCache = readFileSync(new URL("../src/app/useSessionStreamCache.ts", import.meta.url), "utf8");
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
const streamHub = readFileSync(new URL("../../server/internal/api/stream_hub.go", import.meta.url), "utf8");

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
// 466b21e 的契约（附件 token 取 agent_path，缺失时回退 path）**保留**，只是组装点收敛了：
// 2026-10-06 把 4 处重复的 `[file: ${...}]` 拼装收成 `services/upload.ts` 的 `formatFileToken`
// + `fileTokenPath`（冲突⑮a）。所以判据改为：回退逻辑在 `fileTokenPath` 里，App 走它。
assert.match(
  uploadSrc,
  /file\?\.agent_path \|\| file\?\.path/,
  "466b21e agent_path 回退必须在 fileTokenPath 里（附件的唯一取路径点）",
);
assert.match(
  app,
  /formatFileToken\(fileTokenPath\(file\)\)/,
  "466b21e App 必须走唯一的 token 组装点（不得退回内联拼装）",
);

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

// d36cc53 — 前端重连后不能丢掉在途内容
//
// **这条契约的落点在 2026-10-06 反转了，但目的没变。** 上游那版是「重连后从上次的
// 事件序号续流」：`session.ready` 带上客户端自报的 `event_cursor`，服务端只补发缺的那几条。
// 它要求客户端缓存与服务端游标严格同步，而这条不变量在这套系统里维持不住 ——
// 游标在真实运行中恒为空（重放走批处理帧 `payload.events`，写游标的那段只认单条
// `payload.event`；user_message / done / sendMessage 又各删一次），于是每次切会话都是一次
// **全量重发**，而客户端按「增量」去 merge：末条不是 agent 就新开一行，整轮正文堆进去 ——
// 用户看到的就是「ask 卡下面多出一整份正文」。
//
// 现在的契约是**快照重建**：挂载会话一律回一整批带 `reset:true` 的事件，客户端先清空
// 瞬时尾巴再照单应用。它天然幂等，因此**不需要**任何客户端↔服务端的进度同步。
// 下面这几条钉的就是这个新契约 —— 谁想把游标加回来，先读 docs/session-streaming-rework.md。
assert.match(app, /cachedBeforeSync/, "d36cc53 App restore should capture cachedBeforeSync");
assert.match(streamHub, /payload\["reset"\] = true/, "挂载会话的快照帧必须带 reset:true，客户端据此清空瞬时尾巴");
assert.match(streamHub, /buildSessionStreamBatchResponse\(rootID, sessionKey, step\.events, true\)/, "挂载首帧 = 快照（reset）");
assert.match(streamHub, /buildSessionStreamBatchResponse\(rootID, sessionKey, batch, false\)/, "排空续投帧 = 增量（不 reset），否则清掉快照刚重建的尾巴");
assert.match(sessionSvc, /session\.stream\.reset/, "批处理帧必须在 emitDecrypted 里拆成 reset + 单事件，两条分发路径只留一种形状");
// 「形状」与「语义」必须分开判：形状（events 是不是数组）决定**怎么展开**，
// reset 才决定**展开前要不要清尾巴**。绑在一起的话，任何不带 reset 的批帧都会漏回
/// payload.event 路径，重新变成 onStream(undefined)。
assert.match(sessionSvc, /type === "session\.stream" && Array\.isArray\(nextPayload\.events\)/, "批帧判据必须只看形状，不看 reset 标志");
assert.match(sessionSvc, /export function dropTransientExchanges/, "清瞬时尾巴的唯一出口住在 session.ts");
assert.match(sessionSvc, /isTransientExchange/, "瞬时行的判据必须是一个共用谓词：seq 为空且不是用户行");
assert.doesNotMatch(sessionSvc, /eventCursors|getEventCursor|clearEventCursor/, "游标协议已删除，不得回潮");
assert.doesNotMatch(sessionSvc, /event_cursor: eventCursor/, "session.ready 不得再带 event_cursor");
assert.doesNotMatch(sessionSvc, /mergeStreamedText/, "mergeStreamedText 是重投递的适配层，随协议一起删");
assert.doesNotMatch(streamHub, /ReplayPending\(rootID, clientID, sessionKey, eventCursor\)/, "服务端不得再从客户端取进度");
// 2026-10-06：保留本地 seq==0 的规则收敛进 session.ts 的 composeLoadedExchanges
// （原先在 App 里写了四遍、三遍是错的）。契约不变 —— App 侧必须调用它。
assert.match(app, /composeLoadedExchanges\(/, "d36cc53 contract: App restore must keep local seq==0 exchanges (now via composeLoadedExchanges)");
assert.match(sessionSvc, /export function composeLoadedExchanges/, "the load-time composition rule lives in session.ts");
// 2026-09 App.tsx 拆分：userShell 流合并搬到 app/useSessionStreamCache.ts，契约随文件走。
assert.match(streamCache, /replaySnapshot === true/, "d36cc53 App should branch on replaySnapshot when coalescing userShell");

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

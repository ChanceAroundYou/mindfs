import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

/**
 * 关联文件统计改批量接口的契约守卫（`Scope: G-AH`）。
 *
 * 为什么需要它：这一层改动把「N 个文件各发一次 6.5KB 的完整 diff」换成
 * 「一次请求只拿 status/additions/deletions」。行为等价性由 Go 侧
 * `gitview_test.go` 的逐字段对拍 + git 调用次数对拍钉住；这里钉的是前端这半边
 * —— 一旦有人把批量调用改回逐文件循环、去掉去抖、或把「合并」写成「整体替换」，
 * 流量与徽标闪动会一起回来，而单看代码很难发现。
 */

const root = path.resolve(import.meta.dirname, "..");
const read = (relative) => fs.readFileSync(path.join(root, relative), "utf8");

const hook = read("src/hooks/useRelatedFileStats.ts");
const gitService = read("src/services/git.ts");

// 1) 只能走批量：hook 里不得再出现逐文件的 diff 抓取。
assert.match(
  hook,
  /fetchRelatedFileStatsBatch\(rootId, targets, nodeId \|\| undefined\)/,
  "the hook must fetch related-file stats through the batch endpoint",
);
assert.doesNotMatch(
  hook,
  /fetchGitRelatedFileDiff/,
  "the hook must not fall back to per-file diff fetches — that is the request storm this replaced",
);
assert.equal(
  hook.match(/fetchRelatedFileStatsBatch\(/g)?.length,
  1,
  "the hook must issue exactly one batch call, not one per file",
);

// 2) 去抖：refreshKey 由 git status 派生、每次文件变更都会变，没有合并窗口就会
//    每个变更批次重发一次。
assert.match(
  hook,
  /const RELATED_FILE_STATS_DEBOUNCE_MS = 300;/,
  "the refresh window must stay explicit and documented",
);
assert.match(hook, /window\.setTimeout\(/, "the batch fetch must be debounced");
assert.match(
  hook,
  /window\.clearTimeout\(timer\)/,
  "the debounce must be cancelled on re-run/unmount, otherwise a stale batch lands last",
);

// 3) 合并而非整体替换：拉失败或部分命中时，不能把已在显示的徽标抹掉。
//    与本仓库 mergeReplyingStateByNode 同一原则。
assert.match(
  hook,
  /setStatsByKey\(\(prev\) => \(\{ \.\.\.prev, \.\.\.next \}\)\)/,
  "refreshed stats must merge into the previous ones instead of replacing them",
);

// 4) 同一批文件的两处 hook 实例（App.tsx 与 SessionViewer.tsx）必须共用一次请求。
assert.match(
  gitService,
  /const relatedFileStatsInflight = new Map</,
  "the batch fetch needs an in-flight map so the two hook instances share one request",
);
assert.match(
  gitService,
  /relatedFileStatsInflight\.delete\(key\);[\s\S]*?\}\)\s*\.finally|\.finally\(\(\) => \{\s*relatedFileStatsInflight\.delete\(key\);/,
  "the in-flight entry must be cleared in finally, otherwise a failure poisons the key forever",
);

// 5) 端点与节点路由：批量请求必须打新端点，且带上解析后的节点
//    （跨节点同名项目时裸 rootId 会串到别的机器）。
assert.match(
  gitService,
  /appURL\("\/api\/git\/related-files\/stats", undefined, scopedNodeId\)/,
  "the batch request must target the stats endpoint with the resolved node",
);
assert.match(
  gitService,
  /method: "POST",\s*headers: \{ "Content-Type": "application\/json" \},/,
  "the batch request must be a JSON POST",
);

// 6) 空的 status 不进结果 —— 与逐文件写法里 `if (!diff.status) return null` 一致，
//    否则「自基线以来无变更」的文件会渲染出 +0 −0 徽标。
assert.match(
  gitService,
  /if \(!id \|\| !status\) \{\s*continue;\s*\}/,
  "entries without a status must be dropped, not rendered as +0 -0",
);

console.log("related-file-stats-batch.test.mjs: OK");

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

// 服务端删掉的任务不能永远留在客户端缓存里。
//
// 实测：手机上看得到 #11/#13，两台机器的库里都查不到它们，PC 节点也报
// "root not found"。刷新一次就没了 —— 说明那些卡片是 IndexedDB 里的旧记录。
//
// 根因是缓存只写不删：upsertCachedTaskDetails 全程只有 put，App 里的
// applyTaskDetails 只会合并覆盖，整条链路没有任何一处删除。再加上拉取走的是
// 增量（after=newestUpdatedAt），只取更新的行，删除永远不会出现在响应里，
// 于是缓存和内存都把「已经不在服务端的任务」一直渲染下去，跨设备还不一致。
//
// 修法：拉一次全量（不带 after / limit 时服务端返回该 root 全部任务，见
// task_store.go:275），把它当作权威集合 —— 不在集合里的就地淘汰（内存 + 缓存）。

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const tasks = read("src/services/tasks.ts");
const app = read("src/App.tsx");

// —— 缓存层要有一个「按权威集合淘汰」的动作 ——
// 不能只是 put 完就算：没有 delete 的缓存，等于把删除永久吞掉了。
assert.match(
  tasks,
  /export async function pruneCachedTaskDetails\(/,
  "cache must expose a prune entry point; a write-only cache can never forget a deleted task",
);
assert.match(
  tasks,
  /tasks\.delete\(/,
  "prune must actually delete records from the task store",
);
// 淘汰范围必须按 root + node 圈定，不能误伤同 root 在别的节点上的记录。
assert.match(
  tasks,
  /function taskCacheKey\(rootId: string, taskId: string, nodeId\?: string\)/,
  "cacheKey layout must stay the single source of truth for scoping the prune",
);

// —— 内存层要真的把任务从 state 里摘掉 ——
// 只删 IndexedDB 不够：已经渲染进 taskDetailsById 的卡片会继续留在看板上，
// 而 kanbanTasks / kanbanTaskCountItems 都是从它派生的。
assert.match(
  app,
  /pruneTaskDetails\(/,
  "App must prune vanished tasks from in-memory state",
);
assert.match(
  app,
  /Object\.fromEntries\(entries\.filter\(\(\[taskId\]\) => !droppedSet\.has\(taskId\)\)\)/,
  "pruning must actually drop the key from the task map, not just filter a derived array",
);

// —— 淘汰必须按 root 圈定 ——
// taskDetailsById 是跨 root 共享的，权威集合只覆盖本次拉取的那个 root。
// 不判 root_id 的话，刷新 A 项目会把 B 项目的任务整批清掉（比不修还糟）。
assert.match(
  app,
  /const inScope = \(task: KanbanTask\) => task\.root_id === rootId;/,
  "in-memory prune must be scoped to the root that was actually fetched",
);
assert.match(
  app,
  /!keep\.has\(String\(taskId\)\) && inScope\(/,
  "a task is only droppable when it is both absent from the response and in the fetched root",
);
assert.match(
  app,
  /prev\.filter\(\(task\) => !inScope\(task\) \|\| keep\.has\(String\(task\.id\)\)\)/,
  "kanbanTaskCountItems must keep other roots' tasks",
);

// —— 权威集合只能来自全量拉取 ——
// 增量响应（after=…）和限流响应（limit=20）都不是全集，拿它们去淘汰会误删。
// force 路径本来就是拉全量（filters 传 undefined，详见上面那条断言），
// 淘汰挂在这条路上即可，不必再发一次请求。
assert.match(
  app,
  /force \? undefined : \{ after:/,
  "full fetch must drop the after filter so the response is the authoritative set",
);
assert.match(
  app,
  /if \(force\) \{[\s\S]{0,240}?pruneTaskDetails\(/,
  "prune must hang off the force branch, where the response really is the full set",
);
assert.match(
  app,
  /if \(force\) \{[\s\S]{0,320}?pruneCachedTaskDetails\(/,
  "the IndexedDB cache must be pruned on the same authoritative fetch",
);

// —— 刷新必须能发现删除 ——
// 之前 refresh 走的是增量，结构上就看不见「少了谁」，所以永远刷不掉。
assert.match(
  app,
  /const kanbanRefreshSpin = useRefreshSpin\(\(\) => loadKanbanTasks\(currentRootId, true\)\)/,
  "the refresh button must do a full fetch, otherwise deletions can never be observed",
);

// —— 回归：节点隔离不能被这次改动破坏 ——
// 同名项目跨节点时缓存要分节点存，淘汰也必须分节点，否则会删掉另一台机器的记录。
assert.match(
  tasks,
  /records\.filter\(\(r\) => String\(r\.cacheKey \|\| ""\)\.startsWith\(`\$\{nid\}::`\)\)/,
  "node scoping of cached records must be preserved",
);

console.log("task-cache-eviction.test.mjs: OK");

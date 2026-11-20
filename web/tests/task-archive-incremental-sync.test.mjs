// 自动归档要真的到达看板 —— 两条通道缺一不可。
//
// 症状（丢了会怎样）：用户在跑着的实例上等「超过 7 天的任务自动收进归档」，
// 结果看板上那些卡原地不动，看起来就是「自动归档没实现」。两条都不通：
//
//   1. 服务端扫描归档完不广播（那件事不在任何 HTTP 请求里，前端没有别的通知源）；
//   2. 增量拉取只按 updated_at 取变化，而归档刻意不推动 updated_at（它是看板
//      排序键 + 自动归档的兜底起算点），于是 IndexedDB 缓存里那张卡永远停在
//      「完成」列 —— 连重开页面都救不回来。
//
// 第 1 条由 Go 侧 TestSweepAutoArchivedTasksBroadcastsTaskUpdated 钉住；这里钉住
// 客户端这一半：水位要分成两份，请求要真的把 archived_after 发出去。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

function loadModule(relativePath, requireStub = () => ({})) {
  const sourcePath = path.resolve(import.meta.dirname, relativePath);
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2020,
    },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: requireStub,
    // vm 的新 context 不带 Node 的 WHATWG 全局量，crud.ts 拼 query 要用。
    URL,
    URLSearchParams,
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

const cache = loadModule("../src/services/task/cache.ts");

function detail(updatedAt, archivedAt) {
  return { task: { id: "task-1", updated_at: updatedAt, archived_at: archivedAt }, events: [] };
}

test("归档时间是独立水位：updated_at 更新也盖不住它", () => {
  const marks = cache.taskChangeWatermarks([
    detail("2026-11-20T02:50:00Z", ""),
    detail("2026-11-10T00:00:00Z", "2026-11-19T17:31:00Z"),
  ]);
  assert.equal(marks.newestUpdatedAt, "2026-11-20T02:50:00Z");
  assert.equal(
    marks.newestArchivedAt,
    "2026-11-19T17:31:00Z",
    "归档时间必须单独记：合进 updated_at 的 max 之后，落后于它的客户端就再也收不到那条归档",
  );
});

test("增量请求把两条水位都带上；全量刷新不带", () => {
  assert.equal(cache.taskSyncFilters(null, false), undefined, "没有缓存就是全量");
  assert.equal(cache.taskSyncFilters({ newestUpdatedAt: "A", newestArchivedAt: "B" }, true), undefined, "force 就是全量");

  const filters = cache.taskSyncFilters(
    { newestUpdatedAt: "2026-11-20T02:50:00Z", newestArchivedAt: "2026-11-19T17:31:00Z" },
    false,
  );
  assert.equal(filters.after, "2026-11-20T02:50:00Z");
  assert.equal(
    filters.archivedAfter,
    "2026-11-19T17:31:00Z",
    "少了这一条，服务端就只回 updated_at 变化过的行，归档变化永远到不了",
  );
});

test("老缓存 meta 没有归档水位：用纪元补齐一次，别把历史归档漏在过去", () => {
  const filters = cache.taskSyncFilters({ newestUpdatedAt: "2026-11-20T02:50:00Z" }, false);
  assert.equal(
    filters.archivedAfter,
    cache.TASK_ARCHIVE_BACKFILL_WATERMARK,
    "升级前写下的缓存必须补拉一次归档行：它们的 updated_at 不会再动，增量永远够不着",
  );
});

test("fetchTaskDetails 把 archivedAfter 映射成 archived_after 查询参数", async () => {
  const requests = [];
  const crud = loadModule("../src/services/task/crud.ts", (id) => {
    if (id.includes("net/base")) {
      // appURL(path, params, nodeId)：params 是 URLSearchParams，必须拼回去，
      // 否则这条用例测不到请求参数。
      return { appURL: (p, params) => `http://test${p}${params ? `?${params}` : ""}` };
    }
    if (id.includes("net/rootNode")) return { getRootNodeId: () => undefined };
    if (id.includes("net/api")) {
      return {
        protectedJSON: async (url) => {
          requests.push(String(url));
          return { items: [] };
        },
      };
    }
    return {};
  });

  await crud.fetchTaskDetails("mindfs", {
    after: "2026-11-20T02:50:00Z",
    archivedAfter: "2026-11-19T17:31:00Z",
  });
  const url = new URL(requests[0]);
  assert.equal(url.searchParams.get("after"), "2026-11-20T02:50:00Z");
  assert.equal(
    url.searchParams.get("archived_after"),
    "2026-11-19T17:31:00Z",
    "参数名必须与 GET /api/tasks 的解析（archived_after）逐字一致",
  );
});

test("task.updated 带上新的归档标记：工作台要重拉扇出（结构变化）", () => {
  const mod = loadModule("../src/app/realtimeWsTaskHandlers.ts", (id) => {
    if (id.includes("net/error")) return { reportError: () => {} };
    return {};
  });
  const calls = { applied: [], refreshed: 0 };
  const makeCtx = (archivedAt) => ({
    refs: {
      currentRootIdRef: { current: "root" },
      currentRootNodeIdRef: { current: "" },
      taskDetailsByIdRef: { current: { task_1: { task: { id: "task_1", archived_at: archivedAt }, events: [] } } },
      workspaceOpenRef: { current: true },
    },
    setters: { setTaskSessionErrorDialog: () => {} },
    actions: {
      applyTaskDetails: (_root, details) => calls.applied.push(...details),
      pruneTaskDetails: () => {},
      refreshTaskWorktree: () => {},
      refreshWorkspaceBoard: () => { calls.refreshed += 1; },
    },
    values: { t: (key) => key },
  });

  const archivedHandlers = mod.createRealtimeWsTaskHandlers(makeCtx(""));
  archivedHandlers["task.updated"]({}, {
    root_id: "root",
    task: { id: "task_1", updated_at: "2026-11-20T03:00:00Z", archived_at: "2026-11-20T03:00:00Z" },
  });
  assert.equal(calls.applied.length, 1, "卡片内容照旧要换（archived_at 变了）");
  assert.equal(calls.refreshed, 1, "归档是结构变化：工作台按 archived_at 过滤，不重拉的话卡片留在原地");

  // 普通更新（没有归档变化）不能每来一条推送就重拉一次扇出。
  calls.refreshed = 0;
  const plainHandlers = mod.createRealtimeWsTaskHandlers(makeCtx("2026-11-20T03:00:00Z"));
  plainHandlers["task.updated"]({}, {
    root_id: "root",
    task: { id: "task_1", updated_at: "2026-11-20T03:05:00Z", archived_at: "2026-11-20T03:00:00Z" },
  });
  assert.equal(calls.refreshed, 0, "归档标记没变就不该重拉");
});

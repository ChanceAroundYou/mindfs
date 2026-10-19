import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("external-session-import-deadline", async () => {
// 外部会话导入是**长任务**，不能吃 net/api.ts 那个 10 秒的节点存活 deadline。
//
// 实测（2026-10-19，pc-win / codex）：`算法优化` 的 rollout 105 MB、`理论讨论` 160 MB，
// 服务端要整份解析再逐条写 SQLite，耗时随转录大小走。而 NODE_REQUEST_TIMEOUT_MS 是给
// 扇出**读**用的（本机 `/api/sessions?multi_root=1` 实测 100–400ms）。
//
// 套上去的后果不是「导入失败」，而是**客户端在服务端还在干活时先判失败**：会话已经落进
// 列表、请求还在跑，用户却看到一条会自动消失的 `session.import_failed`，于是以为没导进去
// 再点一次，服务端两次都跑完 —— 列表里出现两个同名会话（实测复现）。
//
// 纯源码守卫，不依赖浏览器/网络。

// read: web/ 相对（import.meta.dirname 是 web/tests）
const read = (rel) => fs.readFileSync(path.resolve(import.meta.dirname, "..", rel), "utf8");
const sessionApi = read("src/services/session/sessionApi.ts");
const netApi = read("src/services/net/api.ts");

// 逃生口本身必须在：调用方自带 signal 时，withRequestDeadline 不挂自己的 deadline。
// 本修复走的就是它 —— 没有它就得去改 requestTimeoutMs 的判定口径。
assert.match(
  netApi,
  /typeof AbortController === "undefined" \|\| init\.signal\s*\n\s*\? 0/,
  "withRequestDeadline must yield to a caller-supplied signal — that is the escape hatch this fix relies on",
);

// 导入的超时必须是自己的具名常量，且量级是分钟而不是 10 秒。
const importTimeout = sessionApi.match(/const IMPORT_REQUEST_TIMEOUT_MS = ([^;]+);/);
assert.ok(importTimeout, "the import deadline must be a named constant");
assert.match(
  importTimeout[1],
  /30 \* 60 \* 1000/,
  "the import deadline must be minutes — the 10s node-liveness budget is what produced the false failure",
);

// 两个导入端点都要自带 signal。只修一个会留下另一半照旧误报。
for (const endpoint of ["/api/sessions/import", "/api/sessions/import/batch"]) {
  const at = sessionApi.indexOf(`appURL("${endpoint}"`);
  assert.ok(at > 0, `${endpoint} must be called through appURL`);
  assert.match(
    sessionApi.slice(at, at + 600),
    /signal: AbortSignal\.timeout\(IMPORT_REQUEST_TIMEOUT_MS\)/,
    `${endpoint} must carry its own deadline, or the node-liveness budget reports a false failure`,
  );
}

console.log("external-session-import-deadline.test.mjs: OK");
});

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

/**
 * 列表载荷瘦身 + 条件请求（ETag/304）的契约守卫（`Scope: G-AH`）。
 *
 * 这一层的失败是**静默**的：前端对每个字段都是防御式读取，去错字段不会报错、
 * 只会渲染成空；304 协商写错则表现为「列表再也不刷新」或「每次都多传一份全量」。
 * 所以这里把边界写死，而不是靠肉眼核对。
 */

const root = path.resolve(import.meta.dirname, "..");
// tests/ 的 `..` 是 web/，Go 侧文件在仓库根
const repoRoot = path.resolve(root, "..");
const api = fs.readFileSync(path.join(root, "src/services/api.ts"), "utf8");
const http = fs.readFileSync(path.join(repoRoot, "server/internal/api/http.go"), "utf8");
const httpTasks = fs.readFileSync(path.join(repoRoot, "server/internal/api/http_tasks.go"), "utf8");
const helpers = fs.readFileSync(path.join(repoRoot, "server/internal/api/helpers.go"), "utf8");

// ── 服务端：列表端点必须走 respondJSONList ──────────────────────────
// 三个已接的列表端点：会话列表（单项目 / multi_root）与任务总览。
assert.match(
  http,
  /respondJSONList\(w, r, map\[string\]any\{\s*"items":\s*payload,/,
  "会话列表必须走 respondJSONList",
);
assert.match(
  http,
  /respondJSONList\(w, r, map\[string\]any\{"groups": groups\}\)/,
  "multi_root 会话列表必须走 respondJSONList",
);
assert.match(
  httpTasks,
  /respondJSONList\(w, r, map\[string\]any\{"items": projected\}\)/,
  "任务总览必须走 respondJSONList",
);

// ── 服务端：去空值的边界 ────────────────────────────────────────────
assert.match(helpers, /func isBlankJSONValue/, "isBlankJSONValue must exist");
// `0` 不能被当成空 —— total_count: 0 / dirty_count: 0 可能正是要表达的信息。
assert.match(
  helpers,
  /case bool:\s*\n\s*return !typed/,
  "booleans must be treated as blank when false",
);
assert.ok(
  !/case float64:[\s\S]{0,80}return true/.test(helpers),
  "numeric zero must NOT be treated as blank — total_count: 0 is meaningful",
);
// 数组元素不能被删（位置语义），只递归。
assert.match(
  helpers,
  /case \[\]any:[\s\S]{0,200}?for i, item := range typed \{\s*\n\s*typed\[i\] = stripEmptyJSONValues\(item\)/,
  "array elements must only be recursed into, never deleted",
);
// ETag 算在瘦身之后的字节上，且必须是强判据（同一内容同一 ETag）。
assert.match(helpers, /sum := sha1\.Sum\(body\)/, "ETag must be derived from the stripped body");
assert.match(helpers, /If-None-Match/, "the handler must honour If-None-Match");

// ── 客户端：304 的处理顺序与缓存边界 ────────────────────────────────
// `response.ok` 对 304 是 false，必须在 `!response.ok` 检查之前返回。
{
  const idx304 = api.indexOf("response.status === 304");
  const idxOk = api.indexOf("if (!response.ok) {", api.indexOf("export async function protectedJSON"));
  assert.ok(idx304 > 0, "protectedJSON must handle 304");
  assert.ok(idxOk > idx304, "304 must be handled BEFORE the !response.ok check — otherwise it is reported as a failed request");
}
assert.match(api, /const method = String\(init\.method \|\| "GET"\)\.toUpperCase\(\);/, "the method must be detected");
assert.match(api, /const cacheable = method === "GET";/, "only GET requests may be conditional — writes must never be conditional");
assert.match(api, /headers\.set\("If-None-Match", known\)/, "If-None-Match must be sent when we have a stored ETag");
assert.match(api, /const conditionalRequestMax = 16;/, "the conditional cache needs an explicit bound");
assert.match(api, /forgetConditionalResponse\(url\)/, "a response without ETag must clear the stale entry");

console.log("conditional-request.test.mjs: OK");

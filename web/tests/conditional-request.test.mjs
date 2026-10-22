import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { readGoPackage } from "./go-source.mjs";

test("conditional-request", async () => {
/**
 * 列表载荷瘦身 + 条件请求（ETag/304）的契约守卫（`Scope: G-AN`）。
 *
 * 这一层的失败是**静默**的：前端对每个字段都是防御式读取，去错字段不会报错、
 * 只会渲染成空；304 协商写错则表现为「列表再也不刷新」或「每次都多传一份全量」。
 * 所以这里把边界写死，而不是靠肉眼核对。
 */

const root = path.resolve(import.meta.dirname, "..");
// tests/ 的 `..` 是 web/，Go 侧文件在仓库根
const repoRoot = path.resolve(root, "..");
const api = fs.readFileSync(path.join(root, "src/services/api.ts"), "utf8");
// Go 侧按**包**读，不钉文件名：2026-10-16 的 B1–B10 拆分把 http.go /
// http_sessions.go / http_tasks.go 切成职责文件（http_routes.go、
// http_sessions_list.go、http_task_templates.go…），实现没少、只是搬了家。
const httpDomain = readGoPackage("server/internal/api");
const httpTasks = httpDomain;
// helpers.go 单独读：下面几条断言是带 `[\s\S]{0,80}` 窗口的**负例**，
// 摊到整包上会撞上别的文件里的无关代码而假红。它是单职责小文件，不受拆分影响。
const helpers = fs.readFileSync(path.join(repoRoot, "server/internal/api/helpers.go"), "utf8");

// ── 服务端：列表端点必须走 respondJSONList（瘦身 + 协商）────────────
// 三个已接的列表端点：会话列表（单项目 / multi_root）与任务总览。
assert.match(
  httpDomain,
  /respondJSONList\(w, r, map\[string\]any\{\s*"items":\s*payload,/,
  "会话列表必须走 respondJSONList",
);
assert.match(
  httpDomain,
  /respondJSONList\(w, r, map\[string\]any\{"groups": groups\}\)/,
  "multi_root 会话列表必须走 respondJSONList",
);
assert.match(
  httpTasks,
  /respondJSONList\(w, r, map\[string\]any\{"items": projected\}\)/,
  "任务总览必须走 respondJSONList",
);

// ── 服务端：详情类端点只协商、**不**瘦身 ────────────────────────────
// 这六个端点载荷本身就是详情：git status 的 dirty_count: 0、agents 的空数组、
// 看板任务的 stages/events 流水都是承重信息，套错函数就是静默丢数据。
// 每个断言都同时钉住「接了协商」与「没换成 respondJSONList」两件事 ——
// 只写前者的话，日后有人把它改成 respondJSONList 也照样绿。
for (const [needle, label] of [
  [/respondJSONConditional\(w, r, map\[string\]any\{\s*"agents": statuses,/, "agents 列表"],
  [/respondJSONConditional\(w, r, map\[string\]any\{\s*"agents": \[\]map\[string\]any\{\},\s*"shells": \[\]map\[string\]any\{\},/, "agents 短路空响应"],
  [/respondJSONConditional\(w, r, map\[string\]any\{"entries": \[\]any\{\}\}\)/, "tree 空目录"],
  [/respondJSONConditional\(w, r, map\[string\]any\{\s*"entries": out\.Entries,/, "tree 正常响应"],
  [/respondJSONConditional\(w, r, out\.Status\)/, "git status"],
  [/respondJSONConditional\(w, r, map\[string\]any\{"sessions": \[\]map\[string\]any\{\}\}\),?/, "replying-sessions 空响应"],
  [/respondJSONConditional\(w, r, map\[string\]any\{"sessions": payload\}\)/, "replying-sessions 正常响应"],
]) {
  assert.match(httpDomain, needle, `${label} 必须走 respondJSONConditional`);
}
// http_tasks.go 的两个列表端点：模板库与看板任务。
assert.equal(
  (httpTasks.match(/respondJSONConditional\(w, r, map\[string\]any\{"items": items\}\)/g) || []).length,
  2,
  "task-templates 与看板任务列表都必须走 respondJSONConditional（stages/events 不能瘦）",
);
assert.ok(
  !/respondJSONList\(w, r, out\.Status\)/.test(httpDomain),
  "git status 绝不能用 respondJSONList —— dirty_count: 0 / clean: false 会被删掉",
);
assert.ok(
  !/respondJSONList\(w, r, map\[string\]any\{"sessions": payload\}\)/.test(httpDomain),
  "replying-sessions 绝不能用 respondJSONList",
);

// 单会话详情是全站最重的端点：窗口化尾部拉取（?latest=20）实测单次 1.14 MB，
// 客户端 ~1.7 秒一次，两台机器各量到 60 分钟 4,746 次 ≈ 1.1 GB/h（占全部请求 70%）。
// 必须走**只协商**的那条 —— 载荷里的 exchanges / exchange_aux / window_meta
// 少一个键就是少一段对话。
// 2026-10-22：回包组装抽成 sessionDetailPayload（GET 与 sync 共用一份，避免两处口径分叉），
// 所以断言不再钉「参数是 h.sessionResponse(...)」这个字面形状 —— 换成「handleSessionGet 的
// 函数体里，响应只能经 respondJSONConditional 发出」。意图不变，且更耐受重构。
assert.match(
  httpDomain,
  /func \(h \*HTTPHandler\) handleSessionGet[\s\S]{0,4000}?respondJSONConditional\(w, r, /,
  "单会话详情必须走 respondJSONConditional（1.14 MB × 1.7s 的轮询靠 304 压到零字节）",
);
assert.ok(
  !/respondJSONList\(w, r, h\.sessionResponse\(/.test(httpDomain),
  "单会话详情绝不能用 respondJSONList —— 删空键会破坏 seq 增量与窗口语义",
);
// handleSessionSync（POST，写语义）同样回 sessionResponse，但它**不该**被条件化 ——
// 所以这里只在该函数自己的区间里断言，而不是全文扫。
{
  const fn = httpDomain.slice(httpDomain.indexOf("func (h *HTTPHandler) handleSessionGet"));
  const body = fn.slice(0, fn.indexOf("\nfunc "));
  assert.ok(
    !/respondJSON\(w, http\.StatusOK/.test(body),
    "单会话详情不能退回无条件 respondJSON（那正是 1.1 GB/h 的来源）",
  );
}

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
// 数组元素不能被删（位置语义），只递归成新切片。
assert.match(
  helpers,
  /case \[\]any:[\s\S]{0,200}?for _, item := range typed \{\s*\n\s*out = append\(out, stripEmptyJSONValues\(item\)\)/,
  "array elements must only be recursed into, never deleted",
);
// 必须是**返回副本**而不是原地删键：原地删键时「调用方必须传本请求现造的结构」
// 只是注释里的契约，谁传了共享缓存，那些键就永久消失。
assert.ok(
  !/delete\(typed, key\)/.test(helpers),
  "stripEmptyJSONValues must not mutate its input — shared payloads would lose keys permanently",
);
// ETag 算在瘦身之后的字节上，且必须是强判据（同一内容同一 ETag）。
assert.match(
  helpers,
  /fmt\.Sprintf\("W\/\\"%x\\"", sha1\.Sum\(body\)\)/,
  "ETag must be a weak sha1 of the (stripped) body",
);
assert.match(helpers, /If-None-Match/, "the handler must honour If-None-Match");
// 两条响应路径共用同一段协商逻辑，否则改一处漏一处。
assert.match(helpers, /func writeJSONWithETag/, "both responders must share writeJSONWithETag");
assert.equal(
  (helpers.match(/func writeJSONWithETag/g) || []).length,
  1,
  "there must be exactly one ETag implementation",
);

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
assert.match(api, /const conditionalRequestMax = 64;/, "the conditional cache needs an explicit bound (64: 10 endpoints, several with varying query params)");
// 条目数是**不够**的：单会话详情一条就是 1.14 MB，64 条能把标签页撑爆
// （而标签页崩溃正是这一轮要修的症状）。所以必须另有一条字节预算。
assert.match(
  api,
  /const conditionalRequestBytesMax = /,
  "the conditional cache needs a byte budget on top of the entry cap — one session window is 1.14 MB",
);
assert.match(
  api,
  /rememberConditionalResponse\(url, etag, payload, text\.length\)/,
  "payload size must come from the parsed body (Content-Length is absent: the body is chunked)",
);
// ETag 与载荷必须同生共死：只删载荷会留下「304 但拿不出内容」的悬空条目。
assert.match(
  api,
  /function forgetConditionalResponse\(url: string\): void \{[\s\S]{0,400}?conditionalETagByURL\.delete\(url\);[\s\S]{0,120}?conditionalPayloadByURL\.delete\(url\);/,
  "eviction must drop the ETag and the payload together",
);
// 淘汰循环必须**同时**看条数和字节，只看一个等于没加预算。
assert.match(
  api,
  /conditionalPayloadByURL\.size > conditionalRequestMax \|\|\s*\n\s*conditionalPayloadBytes > conditionalRequestBytesMax/,
  "the eviction loop must honour both the entry cap and the byte budget",
);
assert.match(api, /forgetConditionalResponse\(url\)/, "a response without ETag must clear the stale entry");

console.log("conditional-request.test.mjs: OK");
});

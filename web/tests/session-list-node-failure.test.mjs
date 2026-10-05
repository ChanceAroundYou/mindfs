import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

// 2026-10-02 三个缺陷的回归守卫：
//
//   B1 看板/定时任务新建的会话永不进对话列表 —— 根因是那些路径只广播 session.meta.updated，
//      而前端的该 handler 只更新已缓存条目（sessionCacheRef），不碰 multiProjectSessionGroups。
//      session.created 才是「新会话出现了」的信号，前端 handler 会重拉列表。
//   B2 远端节点断联后整个会话列表不更新 —— 零超时。pc 断联是**挂起**不是快速失败
//      （实测 25s 无响应、http_code=000，DNS 失败只要 0.1s），浏览器 fetch 对
//      「连上了但不回话」没有内置超时（≈2 分钟），Promise.all 扇出被它拖死，
//      其余节点的数据回来了也提交不上去。
//   B3 断联节点的会话显示名字但打不开正文 —— 需要显式的「节点不可达」提示。
//
// 纯源码守卫，不依赖浏览器/网络。

// read: web/ 相对（import.meta.dirname 是 web/tests）
const read = (rel) => fs.readFileSync(path.resolve(import.meta.dirname, "..", rel), "utf8");
// readRepo: 仓库根相对（tests → web → 仓库根）
const readRepo = (rel) => fs.readFileSync(path.resolve(import.meta.dirname, "..", "..", rel), "utf8");

const appcontext = readRepo("server/internal/api/appcontext.go");
const http = readRepo("server/internal/api/http.go");
const scheduled = readRepo("server/internal/scheduled/tasks.go");
const api = read("src/services/api.ts");
const realtime = read("src/app/useRealtimeEvents.ts");
const app = read("src/App.tsx");
const sessionList = read("src/components/SessionList.tsx");
const nodeBadge = read("src/components/NodeBadgeHeader.tsx");
const e2ee = read("src/services/e2ee.ts");

// ---------------------------------------------------------------------------
// B1：所有建会话路径都发 session.created
// ---------------------------------------------------------------------------

// 建会话的三个入口（看板 AppContext、WS、scheduled）都必须广播。
assert.match(
  appcontext,
  /func \(s \*AppContext\) BroadcastSessionCreated\(rootID string, sess \*session\.Session\)/,
  "AppContext must own the session.created broadcast so non-HTTP paths can reach it",
);
assert.match(
  appcontext,
  /Type: "session\.created"/,
  "BroadcastSessionCreated must emit session.created — meta.updated never adds a list row",
);
assert.match(
  appcontext,
  /s\.BroadcastSessionMetaUpdated\(exec\.RootID, created\)\s*\n\s*s\.BroadcastSessionCreated\(exec\.RootID, created\)/,
  "EnsureAgentSession (kanban) must broadcast session.created right after meta.updated",
);
assert.match(
  scheduled,
  /broadcaster\.BroadcastSessionMetaUpdated\(current\.RootID, created\)\s*\n\s*broadcaster\.BroadcastSessionCreated\(current\.RootID, created\)/,
  "scheduled tasks create sessions the same way and need the same signal",
);
assert.match(
  scheduled,
  /OnSubSessionCreated:[\s\S]*?BroadcastSessionCreated\(current\.RootID, created\)/,
  "sub-sessions are new sessions too — they must land in the list under their parent",
);
assert.match(
  scheduled,
  /interface \{[\s\S]*?BroadcastSessionCreated\(rootID string, sess \*session\.Session\)/,
  "the broadcaster interface must expose session.created, or the per-account implementations won't compile",
);

// 已有的 fork 路径仍要能用同一份 payload 构造（不重复造一份形状）。
assert.match(
  http,
  /func sessionListResponse\(s \*session\.Session, resolvedShell string\) map\[string\]any/,
  "the list-row payload must be a package-level function so AppContext can reuse it",
);
assert.match(
  http,
  /Type: "session\.created",\s*\n\s*Payload: map\[string\]any\{\s*\n\s*"root_id": req\.RootID,\s*\n\s*"session": h\.sessionListResponse\(out\.Session\),/,
  "the fork path must keep emitting session.created with the same payload shape",
);

// 坑：AppContext 侧没有 HTTPHandler，shell 的解析必须能独立走。
assert.match(
  http,
  /func \(s \*AppContext\) ResolveCommandShell\(\) string/,
  "the command shell resolution must be reachable from AppContext, not only from HTTPHandler",
);

// ---------------------------------------------------------------------------
// B2：跨机器请求必须有 deadline
// ---------------------------------------------------------------------------

assert.match(
  api,
  /export const NODE_REQUEST_TIMEOUT_MS = \d+;/,
  "cross-node requests need an explicit deadline constant",
);
assert.doesNotMatch(
  api,
  /NODE_REQUEST_TIMEOUT_MS = 0/,
  "the deadline must be non-zero — zero is what made hangs last ~2 minutes",
);

// deadline 只在跨机器时加：本机请求不该有人为上限。
assert.match(
  api,
  /function isCrossMachineRequest\(input: RequestInfo \| URL\): boolean \{[\s\S]*?return !isSameServerAsPage\(raw\);/,
  "the deadline must be scoped to cross-machine requests, not applied to the local server",
);
assert.match(
  api,
  /return isCrossMachineRequest\(input\) \? NODE_REQUEST_TIMEOUT_MS : LOCAL_REQUEST_TIMEOUT_MS;/,
  "requestTimeoutMs must return 0 for same-server requests",
);

// 保护链路上的每个出口都要过 deadline 包装。
for (const fn of ["fetchJSON", "fetchMaybeJSON"]) {
  const body = api.slice(api.indexOf(`export async function ${fn}<`));
  assert.match(
    body.slice(0, 400),
    /await fetchWithDeadline\(input, init\)/,
    `${fn} must fetch through the deadline wrapper`,
  );
}
for (const fn of ["protectedFetch", "protectedJSON"]) {
  // protectedJSON 声明里带泛型 <T>，所以从函数名开始切，不带 "<"
  const start = api.indexOf(`export async function ${fn}`);
  assert.ok(start > 0, `${fn} must exist in services/api.ts`);
  assert.match(
    api.slice(start, start + 400),
    /e2eeProtectedFetchWithDeadline\(input, init\)/,
    `${fn} must fetch through the deadline wrapper`,
  );
}

// 定时器必须清理：AbortController 没有「取消超时」的口子，漏一次就漏一个挂单。
assert.match(
  api,
  /\} finally \{\s*\n\s*if \(deadline\.timer\) clearTimeout\(deadline\.timer\);/,
  "the deadline timer must be cleared in a finally, or every fan-out leaks a pending timer",
);

// abort 是否真的发生必须能被问到（错误名字靠不住，见下），所以 expired 是个可问的闭包。
assert.match(
  api,
  /const timer = setTimeout\(\(\) => \{\s*\n\s*expired = true;\s*\n\s*controller\.abort\(\);/,
  "the abort path must record that it fired, so the error can be classified without trusting its name",
);

// 调用方自带 signal 时不覆盖（那是它自己的取消语义）。
assert.match(
  api,
  /typeof AbortController === "undefined" \|\| init\.signal\s*\n\s*\? 0/,
  "an explicit caller signal must win over our deadline",
);

// 超时要能被识别成「失败」而不是静默的 AbortError。
// 实测过两种形态：浏览器给 AbortError，Node/undici 给 TypeError: fetch failed 包着
// ConnectTimeoutError（和我们的 deadline 撞车，谁先到不确定）—— 只认名字会漏掉后者。
assert.match(api, /export class RequestTimeoutError extends Error/, "timeouts need a recognizable error type");
assert.match(
  api,
  /if \(expired \|\| \(err as any\)\?\.name === "AbortError" \|\| !\(err instanceof APIError\)\) \{/,
  "a network error under a deadline must be reported as a timeout regardless of its runtime-specific name",
);
assert.match(
  api,
  /if \(timeoutMs <= 0\) return err;/,
  "a caller-supplied signal (timeoutMs 0) must be left alone — that is its own cancel semantics",
);

// e2ee 重建 init 时要保留 signal，否则 deadline 挂不上去。
assert.match(
  e2ee,
  /const next: RequestInit = \{ \.\.\.init, method, headers \};/,
  "e2ee must spread the incoming init so our AbortSignal survives its rebuild",
);

// 重试的总代价现在有上界（3 次 × 10s 而不是 3 × 2min）。
assert.match(
  api,
  /const NODE_RETRY_DELAYS_MS = \[400, 1200\];[\s\S]*?每次尝试都被 fetchWithDeadline 的 \d+ 秒 deadline 兜住/,
  "the retry comment must record that the deadline now bounds the total cost",
);

// ---------------------------------------------------------------------------
// B1 前端：created 走合并窗口，且真的重拉列表
// ---------------------------------------------------------------------------

assert.match(
  realtime,
  /"session\.created":[\s\S]*?scheduleMultiProjectSessionReload\(\);/,
  "session.created must trigger a list reload (that's the whole point of the event)",
);
assert.doesNotMatch(
  realtime,
  /"session\.created":[\s\S]*?scheduleMultiProjectSessionReload\(\);[\s\S]{0,400}?void loadMultiProjectSessionGroups\(\);/,
  "session.created must not also fire an un-debounced reload — it is now emitted by every session-creating path",
);
assert.match(
  app,
  /const scheduleMultiProjectSessionReload = useCallback\(\(\) => \{[\s\S]*?\}, MULTI_PROJECT_RELOAD_DEBOUNCE_MS\);/,
  "App must expose a debounced multi-project reload scheduler",
);
assert.match(
  app,
  /const MULTI_PROJECT_RELOAD_DEBOUNCE_MS = \d+;/,
  "the debounce window must be a named constant",
);

// ---------------------------------------------------------------------------
// B3：断联节点要显式提示，而不是让会话看着像坏了
// ---------------------------------------------------------------------------

// 会话侧：合并时给失败节点的分组打标。
assert.match(
  app,
  /if \(failedNids\.has\(nid\)\) \{[\s\S]*?merged\.push\(\{ \.\.\.g, _unreachable: true \} as MultiProjectSessionGroup\);/,
  "a group kept from a failed node must be marked unreachable, not silently presented as fresh",
);
// 类型上要真的有这个字段。
assert.match(
  read("src/app/appSession.ts"),
  /_unreachable\?: boolean;/,
  "MultiProjectSessionGroup must declare the _unreachable flag",
);
// 渲染侧要把标记显示出来。
assert.match(
  sessionList,
  /const groupUnreachable = \(group as any\)\._unreachable === true;/,
  "SessionList must read the unreachable flag",
);
assert.match(
  sessionList,
  /notice=\{groupUnreachable \? t\("sessionList\.nodeUnreachable"\) : undefined\}/,
  "SessionList must show the notice on the group header",
);
assert.match(
  nodeBadge,
  /notice\?: string;/,
  "NodeBadgeHeader must accept a notice",
);
assert.match(
  nodeBadge,
  /\{notice \? \([\s\S]*?\{notice\}[\s\S]*?\) : null\}/,
  "NodeBadgeHeader must render the notice next to the badge",
);

// 提示要收成叹号、点了才出文字（用户 2026-10-05）。
// 原来那段文字常驻在项目名旁边，一屏几十个项目时噪声压过信息 —— 而「有没有
// 问题」扫一眼叹号就够了，说明是点开才要的东西。
assert.match(
  nodeBadge,
  /onClick=\{\(\) => setNoticeOpen\(\(v\) => !v\)\}/,
  "the notice must be clickable — the explanation opens on demand",
);
assert.doesNotMatch(
  nodeBadge,
  /title=\{notice\}[\s\S]{0,400}?fontSize: "10px"[\s\S]{0,200}?\{notice\}[\s\S]{0,200}?<\/span>\s*\)\s*:\s*null\}[\s\S]{0,400}var\(--text-secondary\)/,
  "the notice text must not sit permanently next to the project name (that is the noise being removed)",
);
// 叹号本身要是个真按钮（可聚焦、有点击语义），不是装饰性 span。
assert.match(
  nodeBadge,
  /aria-label=\{notice\}[\s\S]{0,200}?aria-expanded=\{noticeOpen\}/,
  "the marker needs button semantics so it is reachable by keyboard and announced",
);

// 工作台侧：失败节点要报出来。
assert.match(
  read("src/app/useWorkspaceBoard.ts"),
  /unreachableNodes: Array<\{ id: string; name: string \}>;/,
  "the board must expose which nodes it could not reach",
);

// 两条提示都要有中英文，且 workspace 那条带占位符。
const zh = read("src/i18n/locales/zh-CN.ts");
const en = read("src/i18n/locales/en-US.ts");
assert.match(zh, /"sessionList\.nodeUnreachable": "[^"]+"/, "zh-CN needs the session-list notice");
assert.match(en, /"sessionList\.nodeUnreachable": "[^"]+"/, "en-US needs the session-list notice");
assert.match(zh, /"task\.workspaceNodeUnreachable": "[^"]*\{names\}[^"]*"/, "zh-CN needs the workspace notice with a placeholder");
assert.match(en, /"task\.workspaceNodeUnreachable": "[^"]*\{names\}[^"]*"/, "en-US needs the workspace notice with a placeholder");

console.log("session-list-node-failure.test.mjs: OK");

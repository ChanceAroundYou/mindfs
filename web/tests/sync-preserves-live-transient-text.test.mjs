import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const start = app.indexOf("const localTransientTail = (() => {");
const end = app.indexOf("const normalized = {", start);
const body = app.slice(start, end);

assert.ok(start >= 0 && end > start, "handleSyncSession's transient-tail block should be found");
assert.ok(body.includes("syncedExchanges"), "the block should compare against the synced exchanges");

// 同步会把整个会话灌回来（实测缓存 24 → 92），但本轮尚未落盘的内容只存在于缓存的
// seq=0 瞬时条目里。原先这里只保留 role=tool，导致「点同步后 ask 卡还在、直播正文消失」
// （实测 cacheBeforeTransient=4 只保住 1 条，丢的 3 条正是非 tool 的正文）。
assert.doesNotMatch(
  body,
  /toLowerCase\(\)\s*!==\s*"tool"\)\s*\{\s*return false;/,
  "non-tool transient entries must not be dropped wholesale",
);

// 两类各自的去重依据。
assert.match(
  body,
  /if \(role === "tool"\) \{[\s\S]*?present\.has\(callId\)/,
  "tool transients should still dedupe by callId",
);
assert.match(
  body,
  /presentText/,
  "non-tool transients should dedupe by an explicit presence set",
);
assert.match(
  body,
  /presentText\.has\(`\$\{role\}\|\$\{content\}`\)/,
  "non-tool transients should dedupe by role+content",
);

// 空内容不入，避免把无正文的占位条目塞回去。
assert.match(body, /if \(!content\) return false;/, "empty-content transients should be skipped");

// 拿到手的瞬时条目必须真的并回缓存。
assert.match(
  app,
  /exchanges: \[\.\.\.syncedExchanges, \.\.\.localTransientTail\]/,
  "the transient tail must be appended to the synced exchanges",
);

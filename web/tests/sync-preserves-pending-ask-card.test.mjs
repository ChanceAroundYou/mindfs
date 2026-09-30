import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const syncHandler = app.slice(
  app.indexOf("const handleSyncSession = useCallback("),
  app.indexOf("const handleForkAgentMessage = useCallback("),
);

assert.ok(syncHandler.length > 0, "handleSyncSession should be found in App.tsx");

// 正在等待回答的 ask_user 卡是纯内存态：只存在于前端 sessionCacheRef（WS tool_call
// 追加的 seq=0 role=tool 条目）与服务端 manager.pendingToolCalls 里。服务端**从不**经
// HTTP 下发它（session.Exchange 结构体没有 ToolCall 字段），而 syncSession 以 IndexedDB
// 缓存为 base —— 那里从来没有这些瞬时条目。所以点「同步」时必须从内存缓存把它们捞回来，
// 否则卡片凭空消失（2026-09-12 症状 2）。
assert.match(
  syncHandler,
  /sessionCacheRef\.current\[cacheKey\][\s\S]{0,160}?exchanges/,
  "sync should read the in-memory session cache (IDB base never holds the transient tool card)",
);
// 2026-09-13 更正：原先这里断言「只保留 seq=0 的 tool 条目」，那条假设本身就是缺陷——
// 同一个 seq=0 集合里还有本轮尚未落盘的**直播正文**（role=agent），只留 tool 会让
// 「点同步后 ask 卡还在、正文消失」（实测 cacheBeforeTransient=4 只保住 1 条）。
// 现在两类都保留，各自的去重判据见 sync-preserves-live-transient-text.test.mjs。
assert.match(
  syncHandler,
  /Number\(\(ex as any\)\?\.seq \|\| 0\) !== 0\) return false;/,
  "only seq=0 transient entries should be carried over (persisted rows come back from the server)",
);
assert.match(
  syncHandler,
  /exchanges: \[\.\.\.syncedExchanges, \.\.\.localTransientTail\]/,
  "the transient tail should be merged back into the synced exchange list",
);

// 去重：服务端已带同一 callId（落盘后出现在 exchange_aux / exchanges 里）时不得重复渲染。
assert.match(
  syncHandler,
  /present\.has\(callId\)/,
  "carried-over transients should be de-duplicated by callId",
);
assert.match(
  syncHandler,
  /toolCall\?\.callId/,
  "de-duplication should look at the tool exchange's callId",
);
assert.match(
  syncHandler,
  /toolcall\?\.callId/,
  "de-duplication should also look at the window aux's toolcall.callId",
);

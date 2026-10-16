import assert from "node:assert/strict";
import { test } from "vitest";
import {
  appSrc,
  sessionSrc,
  stripComments,
} from "./session-window-sources.mjs";
// 加载/切会话时的合并与清理规则：切会话闪的根因、mergeSessionExchanges、
// composeLoadedExchanges（加载时的唯一组装规则）、重放适配层已删、dropTransientExchanges。

test("session-window-merge", async () => {
// ── 切会话闪一下（2026-10-05 发现，2026-10-06 定根因）────────────────────
// 症状：切回**运行中**的会话时，先从缓存渲染出完整对话 → 塌到最后一条用户 prompt
// → 再一点点把 assistant 补回来（assistant 越多越明显）。已结束的会话怎么切都不闪。
//
// 根因：加载时要知道「本地在途内容（seq=0）不能丢」，而服务端**从不**下发它们。
// 这条规则曾被写了四遍，其中三遍都去服务端回包找瞬时行 —— 恒取空数组，于是每次
// 加载都把在途内容丢一次。现在收敛成 composeLoadedExchanges 一个定义。
//
// 行为断言在上面的 composeLoadedExchanges 那组；这里只钉「两个分支都走同一条规则」
// 以及「旧写法已彻底消失」。
assert.equal(
  (stripComments(appSrc).match(/composeLoadedExchanges\(/g) || []).length,
  2,
  "restoreActiveSession 的窗口分支与 fallback 分支都必须走同一个组装规则",
);
// 反向断言：旧写法必须彻底消失。断言形状在这里是恰当的 —— 我们禁的是一个**写法**，
// 而它造成的可见缺陷已由上面的行为断言守住。
assert.doesNotMatch(
  appSrc,
  /hasPendingTurn/,
  "hasPendingTurn 恒为 false（服务端回包不带 seq=0）—— 这个守卫必须删掉，不能再有第二份实现",
);
assert.doesNotMatch(
  appSrc,
  /transientTail\s*=\s*winExs\.filter/,
  "瞬时行不能取自服务端窗口回包 —— 那里一个 seq=0 都没有",
);
// aux 必须一并并入：exchanges 保住历史而 exchange_aux 只剩窗口那份时，历史行上的
// 工具卡会凭空消失（按 seq 覆盖，不是拼接 —— 拼接会把同一张卡渲染两遍）。
assert.match(
  appSrc,
  /exchange_aux: \{\s*\n\s*\.\.\.\(\(cachedBeforeSync as any\)\?\.exchange_aux \|\| \{\}\),\s*\n\s*\.\.\.\(sess\.exchange_aux \|\| \{\}\),\s*\n\s*\},/,
  "exchange_aux must merge per-seq too, or restored history loses its tool cards",
);
assert.match(
  sessionSrc,
  /exchanges: mergeSessionExchanges\(baseExchanges, incomingExchanges\),/,
  "appendSessionDelta must use the seq-deduping merge, not concatenation",
);

// 合并函数本体**直接跑**：三条可见集来源（缓存 / 服务端窗口 / loadMore 页）互相
// 重叠，纯拼接会把同一行渲染两遍（实测 2026-10-05：切会话后历史里每条消息出现两次）。
// 这条一旦坏了症状是「内容重复」，源码正则看不出来 —— 所以断言行为，不断言形状。
//
// task-35 拆分后 `mergeSessionExchanges` 住在 services/sessionExchangeMerge.ts
// （session.ts 只是 re-export），所以走 Vitest 的动态 import 而不是 vm 导出 ——
// 与 session-window.test.mjs 里的 `computeTailOverlay` 同一个先例：真实实现，不同构副本。
const sessionMod = await import("../src/services/sessionExchangeMerge.ts");
const mergeSessionExchanges = sessionMod.mergeSessionExchanges;
assert.equal(typeof mergeSessionExchanges, "function", "mergeSessionExchanges must be exported");

// 重叠：缓存 1..5，窗口 4..8 → 1..8 各一行，不重不漏
const overlapped = mergeSessionExchanges(
  [1, 2, 3, 4, 5].map((seq) => ({ seq, content: "old" })),
  [4, 5, 6, 7, 8].map((seq) => ({ seq, content: "new" })),
);
// 注意用 JSON 比而不是 deepEqual：vm 里造出来的对象原型来自另一个 realm，
// deepEqual 的原型检查会因此判它们「结构相同但不相等」。
assert.equal(
  JSON.stringify(overlapped.map((ex) => ex.seq)),
  JSON.stringify([1, 2, 3, 4, 5, 6, 7, 8]),
  "overlapping sources must merge into one row per seq",
);
assert.equal(
  overlapped.filter((ex) => ex.seq === 4).length,
  1,
  "a seq present in both sources must render once, not twice",
);
// 同 seq 冲突：后到者赢（窗口那份更新）
assert.equal(overlapped.find((ex) => ex.seq === 4).content, "new");

// seq=0 的瞬时行必须被丢掉 —— 它们归 overlay 管，混进窗口会与 overlay 重复渲染
assert.equal(
  JSON.stringify(
    mergeSessionExchanges([{ seq: 0, content: "live" }, { seq: 1, content: "a" }], []).map(
      (ex) => ex.seq,
    ),
  ),
  JSON.stringify([1]),
  "seq=0 transient rows must be dropped (overlay owns them)",
);

// 空/垃圾输入不抛
assert.equal(mergeSessionExchanges(null, null).length, 0);
assert.equal(mergeSessionExchanges(undefined, [{ seq: 3 }]).length, 1);

// 乱序输入必须按 seq 升序返回（调用方不自己维护顺序）
assert.equal(
  JSON.stringify(mergeSessionExchanges([], [{ seq: 9 }, { seq: 2 }, { seq: 5 }]).map((e) => e.seq)),
  JSON.stringify([2, 5, 9]),
  "the merge must sort by seq — callers rely on order",
);

// ── composeLoadedExchanges：加载时的唯一组装规则 ───────────────────────────
// 这条规则曾经被写了 4 遍（restoreActiveSession 的窗口/fallback 两处 + 各自的
// resumeCursor 块 + handleSyncSession），其中 3 遍都去**服务端回包**找瞬时行 ——
// 服务端窗口回包只含已落盘行，于是每次加载都把在途内容丢一次。
// 实测 2026-10-06：切回运行中的会话，缓存 376 条（其中 361 条 seq=0），
// 加载后缓存被写成 15 条，界面塌到最后一条用户 prompt，再等直播流补回来（4~6 秒）。
// 断言行为，因为「丢了多少」源码正则看不出来。
const composeLoadedExchanges = sessionMod.composeLoadedExchanges;
assert.equal(
  typeof composeLoadedExchanges,
  "function",
  "composeLoadedExchanges must be exported — it is the single load-time rule",
);

// ① 只有服务端行（冷缓存）：原样返回（在途与否都一样）
for (const inFlight of [true, false]) {
  assert.equal(
    JSON.stringify(
      composeLoadedExchanges([1, 2, 3].map((seq) => ({ seq })), [], inFlight).map((e) => e.seq),
    ),
    JSON.stringify([1, 2, 3]),
    `cold cache must return the server rows (inFlight=${inFlight})`,
  );
}

// ② 在途（会话正在跑）：服务端只回已落盘行，缓存里的 seq=0 必须**全部接回来**
//    （这是「切回运行中会话塌陷」的根因断言 —— 少接一条就是一次可见的塌陷）
{
  const server = [1, 2, 3].map((seq) => ({ seq }));
  const cached = [1, 2, 3].map((seq) => ({ seq })).concat([
    { seq: 0, role: "user", content: "刚发出还没落库" },
    { seq: 0, role: "agent", content: "正在流式输出的正文" },
  ]);
  const out = composeLoadedExchanges(server, cached, true);
  assert.equal(out.length, 5, "the in-flight rows must survive the load (root cause)");
  assert.equal(out.filter((e) => e.seq === 0).length, 2, "both transient rows kept");
  assert.equal(
    JSON.stringify(out.filter((e) => e.seq > 0).map((e) => e.seq)),
    JSON.stringify([1, 2, 3]),
    "persisted rows stay sorted and deduped",
  );
}

// ③ **不在途（会话已结束 / 刚 compact 过）：seq=0 必须全部丢掉**
//    这是另一半 —— 服务端已经把那一轮落盘、或 compact 把历史整个重置了，
//    本地那份 seq=0 从此再也对不上。不清就会跨多次 compact 无声堆积
//    （实测一个会话堆到 1100+ 条：tool=700、thought=360），
//    一旦被渲染出来就是「同一段正文出现两遍、而且每切一次越多」。
{
  const server = [1, 2, 3].map((seq) => ({ seq }));
  const cached = [1, 2, 3].map((seq) => ({ seq })).concat([
    { seq: 0, role: "agent", content: "上一轮的残留" },
    { seq: 0, role: "tool", content: "上一轮的残留工具卡" },
  ]);
  const out = composeLoadedExchanges(server, cached, false);
  assert.equal(out.length, 3, "no in-flight turn ⇒ every seq=0 row is stale residue");
  assert.equal(out.filter((e) => e.seq === 0).length, 0, "stale transients must be dropped");
  assert.equal(
    JSON.stringify(out.map((e) => e.seq)),
    JSON.stringify([1, 2, 3]),
    "only the persisted rows survive",
  );
}

// ④ 缓存更全（用户翻过历史）：缓存里比窗口老的持久行不能丢
{
  const out = composeLoadedExchanges(
    [{ seq: 8 }, { seq: 9 }],
    [1, 8, 9].map((seq) => ({ seq })).concat([{ seq: 0, content: "x" }]),
    true,
  );
  assert.equal(
    JSON.stringify(out.filter((e) => e.seq > 0).map((e) => e.seq)),
    JSON.stringify([1, 8, 9]),
    "older persisted rows from the cache must be kept (load is monotonic)",
  );
  assert.equal(out.filter((e) => e.seq === 0).length, 1);
}

// ⑤ 同一个瞬时对象两侧都有 → 只留一份（按对象同一性）
{
  const shared = { seq: 0, content: "同一条" };
  const out = composeLoadedExchanges([{ seq: 1 }, shared], [{ seq: 1 }, shared], true);
  assert.equal(out.length, 2, "the same transient object must not be duplicated");
}

// ⑥【2026-10-06 反转】在途 + 缓存里跨过 compact → **一条都不许丢**
//    旧行为是「只保留最后一次 compact 之后的瞬时行」，依据是「compact 是服务端历史的重置点」。
//    核对服务端后该依据为假：交换 JSONL 是 append-only（唯一 `os.Remove` 在两处
//    `DeleteSession`），compact 只写一条 `CompactNotice` aux。而 compact 是**轮内**事件，
//    按它切会丢掉本轮 compact 之前那些还没落盘的内容（服务端没有替代品）。
//    陈旧瞬时行由另外两处负责：一轮结束 / reset 的 `dropTransientExchanges`，
//    以及加载时 `inFlight=false`。见 docs/message-mechanisms.md 冲突⑨、tests 的【红·⑨b/⑨c】。
{
  const cached = [
    { seq: 1 },
    { seq: 0, role: "agent", content: "compact 之前的旧内容" },
    { seq: 0, role: "tool", content: "compact 之前的旧工具卡" },
    { seq: 0, role: "compact", content: "" },
    { seq: 0, role: "agent", content: "compact 之后的新内容" },
  ];
  const out = composeLoadedExchanges([{ seq: 1 }], cached, true);
  const transients = out.filter((e) => e.seq === 0);
  assert.equal(
    transients.length,
    4,
    "compact 不是服务端历史的重置点 ⇒ 在途时不能按它切掉瞬时行",
  );
  assert.equal(
    JSON.stringify(transients.map((e) => String(e.content || ""))),
    JSON.stringify(["compact 之前的旧内容", "compact 之前的旧工具卡", "", "compact 之后的新内容"]),
    "瞬时行必须保持缓存顺序（数组顺序即时间序）",
  );
}

// ⑦ 没有 compact 行时不受影响（全部保留 —— 不能把正常情况也砍了）
{
  const cached = [
    { seq: 0, role: "agent", content: "a" },
    { seq: 0, role: "tool", content: "b" },
  ];
  const out = composeLoadedExchanges([{ seq: 1 }], cached, true);
  assert.equal(out.filter((e) => e.seq === 0).length, 2, "没有 compact 边界就不该砍任何东西");
}

// ⑧ 空/垃圾输入不抛
assert.equal(composeLoadedExchanges(null, null, true).length, 0);
assert.equal(composeLoadedExchanges(undefined, undefined, false).length, 0);

// ── 正文拼接不再需要重放适配层（mergeStreamedText 已删除）──────────────────
// 曾被 mergeStreamedText 挡住的形态：服务端把在途回合的内容重推一遍，客户端
// 无条件拼接 → 同一段正文在**行内**变成 `aabb`（「每切一次越多」）。
//
// 根因不在拼接，在**协议**：上游那版是「带 event_cursor 续流」，靠客户端缓存与
// 服务端游标严格同步来保证「每片只投一次」。这条不变量维持不住（游标恒为空，
// 实际每次切会话都是全量重发），于是才需要在拼接处用子串包含去猜「这片是不是重发」。
//
// 现在协议改成**快照重建**：会话挂载一律先清瞬时尾巴（dropTransientExchanges），
// 再照单应用整批事件。重放不再是「增量」，因此拼接就是拼接 —— 无条件的
// `String(last.content) + String(content)` 即正确。
//
// 这条钉子守的是「别把适配层加回来」：一旦有人重新引入按内容猜重发的逻辑，
// 说明协议又退化成了增量续流，那时真正该修的是协议，不是拼接。
assert.equal(sessionMod.mergeStreamedText, undefined, "mergeStreamedText 是重投递适配层，随游标协议一起删除，不得回潮");

// ── dropTransientExchanges：快照重建前的清理出口 ──────────────────────────
// 判据是共用的 isTransientExchange：**seq 为空即瞬时**（2026-10-06 起用户行不再例外）。
// 曾经 user 行例外（怕刚发出去的消息闪一下），但那条例外让「同一条行该不该留」有两把尺
// （`mergeSessionExchanges` 只收 seq>0），去留取决于先跑哪条路径 —— 冲突①。
// 现在两边都丢：丢的那一刻它已有替代品（用户行在回合开始就落盘；reset 在窗口装好之后），
// 而保留它的代价是认领漏掉时与服务端持久行同时渲染。回归护栏：E2E 的
// 「用户消息在时间线里恰好出现 1 次」。
{
  const drop = sessionMod.dropTransientExchanges;
  assert.equal(typeof drop, "function", "dropTransientExchanges must be exported");
  assert.equal(typeof sessionMod.isTransientExchange, "function", "isTransientExchange 是唯一判据，必须导出");
  // ① seq=0 的 assistant/tool/thought 全清，seq>0 的持久行全留
  const session = {
    key: "s",
    exchanges: [
      { seq: 1, role: "user" },
      { seq: 0, role: "agent" },
      { seq: 2, role: "agent" },
      { seq: 0, role: "tool" },
    ],
  };
  const dropped = drop(session);
  assert.equal(
    JSON.stringify(dropped.exchanges.map((e) => e.seq)),
    JSON.stringify([1, 2]),
    "dropTransientExchanges must remove every seq=0 non-user row",
  );
  // ② seq=0 的乐观 user 行**也一起清**（与 mergeSessionExchanges 同判；见上面那段）
  {
    const withEcho = drop({ key: "s", exchanges: [{ seq: 0, role: "user" }, { seq: 0, role: "agent" }] });
    assert.equal(withEcho.exchanges.length, 0, "seq=0 的用户行不再是例外");
    assert.equal(drop({ key: "s", exchanges: [{ seq: 0, role: "user" }] }).exchanges.length, 0);
    // ③ 谓词本身：大小写无关；缺字段、非数字 seq 都归到「瞬时」
    assert.equal(sessionMod.isTransientExchange({ seq: 0, role: "user" }), true);
    assert.equal(sessionMod.isTransientExchange({ seq: 0, role: "User" }), true);
  }
  assert.equal(sessionMod.isTransientExchange({ seq: 3, role: "agent" }), false);
  assert.equal(sessionMod.isTransientExchange({ role: "agent" }), true, "缺 seq 视为瞬时");
  assert.equal(sessionMod.isTransientExchange({ seq: "x", role: "agent" }), true, "非数字 seq 视为瞬时");
  assert.equal(sessionMod.isTransientExchange(null), true);
  // ④ 没有瞬时行时原样返回（同一引用，避免无意义的重渲染）
  const clean = { key: "s", exchanges: [{ seq: 1 }] };
  assert.equal(drop(clean), clean, "no-op must return the same reference");
  assert.equal(drop(null), null);
}
console.log("session-window-merge.test.mjs: OK");
});

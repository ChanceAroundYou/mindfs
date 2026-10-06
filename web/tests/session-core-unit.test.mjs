// 消息核心的**行为**单元测试（不是源码文本匹配）。
//
// 这个文件里的判据全部来自 `src/services/session.ts` 的**导出纯函数**，直接 import 跑。
// 与仓库里 70 个「读源码文本 + 正则」的契约测试是两个层次：
//   · 源码契约测试钉的是「写法」——它红了只说明写法变了，不说明行为变了；
//   · 本文件钉的是「行为」——它红了说明消息的组装结果变了。
// 两者都要有：写法会被后续重构正当改动，行为不该。
//
// 覆盖的机制（见 docs/message-mechanisms.md 的「缓存与组装」一节）：
//   · isTransientExchange      —— 瞬时行的**唯一**谓词
//   · dropTransientExchanges   —— 退役瞬时行（done / compact / reset 三处调用）
//   · mergeSessionExchanges    —— 按 seq 合并（persisted 的合并规则）
//   · composeLoadedExchanges   —— 会话加载的**唯一**组装规则
import { register } from "node:module";
import assert from "node:assert/strict";
import test from "node:test";

register("./ts-module-hook.mjs", import.meta.url);

const {
  isTransientExchange,
  dropTransientExchanges,
  mergeSessionExchanges,
  composeLoadedExchanges,
  appendExchangeAuxDelta,
  toPersistentSession,
} = await import("../src/services/session.ts");

/** 造一条 exchange。seq 省略即「瞬时行」。 */
const ex = (role, content, seq) =>
  seq === undefined ? { role, content } : { role, content, seq };
/** 造一条已持久化行。 */
const persisted = (role, content, seq) => ({ role, content, seq });
/** 造一条会话。 */
const sess = (exchanges) => ({ key: "k", session_key: "k", exchanges });

// ─────────────────────────────────────────────────────────────────────────────
// 机制一：isTransientExchange —— 瞬时行的唯一谓词
// ─────────────────────────────────────────────────────────────────────────────
test("isTransientExchange：seq>0 是持久行，不是瞬时行", () => {
  assert.equal(isTransientExchange(persisted("agent", "a", 1)), false);
  assert.equal(isTransientExchange(persisted("user", "u", 7)), false);
});

test("isTransientExchange：seq 为空的 agent/thought/tool/todo/plan/compact 都是瞬时行", () => {
  for (const role of ["agent", "assistant", "thought", "tool", "todo", "plan", "compact"]) {
    assert.equal(isTransientExchange(ex(role, "x")), true, `${role} 应为瞬时行`);
  }
});

test("isTransientExchange：seq 为空的用户行**也是**瞬时行（2026-10-06 反转）", () => {
  // 曾经 user 行例外，理由是「乐观回声没有 seq，清掉会让刚发出去的消息当场消失」。
  // 那条理由站不住，而且造成两把尺：`dropTransientExchanges` 保留它、`mergeSessionExchanges`
  // 丢弃它 ⇒ 同一条行的去留取决于先跑哪条路径（冲突①）。
  // 现在两边都丢：丢的那一刻它已有替代品（用户行在回合开始就落盘；reset 在窗口装好之后），
  // 而保留它的代价是认领漏掉时与服务端持久行同时渲染（同一句用户消息两遍）。
  assert.equal(isTransientExchange(ex("user", "刚发出去的")), true);
  assert.equal(isTransientExchange(ex("USER", "大小写不敏感")), true);
});

test("isTransientExchange：seq 缺省 / 非数字 / 0 / 负数 / NaN 一律算瞬时（用户行除外）", () => {
  for (const seq of [0, -1, NaN, "abc", null]) {
    assert.equal(isTransientExchange(ex("agent", "x", seq)), true, `seq=${String(seq)}`);
  }
});

// ─────────────────────────────────────────────────────────────────────────────
// 机制二：dropTransientExchanges —— 退役瞬时行
// ─────────────────────────────────────────────────────────────────────────────
test("dropTransientExchanges：清掉 seq=0 的 agent/thought/tool，留下持久行", () => {
  const before = sess([
    persisted("user", "问题", 1),
    ex("agent", "在途正文"),
    ex("thought", "在途思考"),
    persisted("agent", "已落盘正文", 2),
  ]);
  const after = dropTransientExchanges(before);
  assert.deepEqual(
    after.exchanges.map((e) => e.content),
    ["问题", "已落盘正文"],
  );
});

test("dropTransientExchanges：seq=0 的用户行**也一起清**（与 mergeSessionExchanges 同判）", () => {
  // 与上面同一条反转。乐观回声的可见期收敛为「发出 → 被认领（拿到真 seq）」；
  // 被清掉时服务端那条持久行已经在窗口里，所以用户看不到「消息消失」。
  const after = dropTransientExchanges(sess([ex("user", "乐观回声"), ex("agent", "在途")]));
  assert.deepEqual(after.exchanges.map((e) => e.content), []);
});

test("dropTransientExchanges：没有瞬时行时返回**同一引用**（避免无谓的重渲染）", () => {
  const before = sess([persisted("user", "u", 1), persisted("agent", "a", 2)]);
  assert.equal(dropTransientExchanges(before), before);
});

// ─────────────────────────────────────────────────────────────────────────────
// 机制三：mergeSessionExchanges —— persisted 的合并规则
// ─────────────────────────────────────────────────────────────────────────────
test("mergeSessionExchanges：按 seq 升序，且只收 seq>0 的行", () => {
  const merged = mergeSessionExchanges(
    [persisted("agent", "b", 2), ex("agent", "瞬时")],
    [persisted("agent", "a", 1), persisted("agent", "c", 3)],
  );
  assert.deepEqual(merged.map((e) => [e.seq, e.content]), [[1, "a"], [2, "b"], [3, "c"]]);
});

test("mergeSessionExchanges：同 seq 时 incoming 覆盖 base", () => {
  const merged = mergeSessionExchanges(
    [persisted("agent", "旧", 1)],
    [persisted("agent", "新", 1)],
  );
  assert.deepEqual(merged.map((e) => e.content), ["新"]);
});

// ─────────────────────────────────────────────────────────────────────────────
// 机制四：composeLoadedExchanges —— 加载时的唯一组装规则
// ─────────────────────────────────────────────────────────────────────────────
test("composeLoadedExchanges：不在途 ⇒ 只留持久行，缓存里的 seq=0 全部丢弃", () => {
  const out = composeLoadedExchanges(
    [persisted("user", "u", 1)],
    [ex("agent", "陈旧在途正文"), persisted("agent", "a", 2)],
    false,
  );
  assert.deepEqual(out.map((e) => e.content), ["u", "a"]);
});

test("composeLoadedExchanges：在途 ⇒ 把 seq=0 接回（缓存在前，按对象同一性去重）", () => {
  const shared = ex("agent", "共享对象");
  const out = composeLoadedExchanges(
    [persisted("user", "u", 1), shared],
    [shared, ex("agent", "只在缓存")],
    true,
  );
  const contents = out.map((e) => e.content);
  assert.deepEqual(contents, ["u", "共享对象", "只在缓存"]);
  // 同一个对象两侧都在时只能出现一次
  assert.equal(contents.filter((c) => c === "共享对象").length, 1);
});

// 2026-10-06 反转：旧的「只保留最后一次 compact 之后的行」已被删除 —— 前提是假的
// （服务端交换 JSONL append-only，compact 只写一条 CompactNotice aux），而且它会丢掉
// 「本轮跑到一半 compact」时本轮的前半段。行为断言见下面那条【红·⑨b】。
test("composeLoadedExchanges：**不得**再按 compact 边界切瞬时行（本轮的行一条都不许丢）", () => {
  const out = composeLoadedExchanges(
    [],
    [
      ex("agent", "第一次 compact 前"),
      ex("compact", "第一次 compact"),
      ex("agent", "两次 compact 之间"),
      ex("compact", "第二次 compact"),
      ex("agent", "第二次 compact 之后"),
    ],
    true,
  );
  assert.deepEqual(
    out.map((e) => e.content),
    ["第一次 compact 前", "第一次 compact", "两次 compact 之间", "第二次 compact", "第二次 compact 之后"],
    "compact 不是服务端历史的重置点 ⇒ 不能按它切掉在本轮产生的行",
  );
});


// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】两条退役路径对同一行的判决必须一致
//
// 这是第二步要求的「一定会红的测试」的第一条：它钉的不是某个写法，而是一个**不变量** ——
// 「一条 seq=0 的用户行该不该留」这件事，系统里只能有一个答案。
//
// 现状（2026-10-06，session.ts:2119 与 :2029 各判一次、且判法相反）：
//   · dropTransientExchanges（done / compact / reset 三处调用）→ **保留** seq=0 用户行
//   · composeLoadedExchanges（inFlight=false，即每次加载落盘会话）→ 经 mergeSessionExchanges
//     只收 seq>0 ⇒ **丢弃** seq=0 用户行
//
// 后果是「刚发出去的消息会不会消失」取决于先跑哪条路径：先加载后 done 就留下，
// 先 done 后加载就消失。它正是「同一份数据两个机制各判一次」的最小可证伪形态。
//
// 修法见第三步（两泳道：瞬时行不再是「行数组里的一种行」，这条不变量就不必再靠两处对齐）。
test("【红】seq=0 的用户行在两条退役路径上必须得到同一个结论", () => {
  const echo = ex("user", "乐观回声");
  const keptByDrop = dropTransientExchanges(sess([echo]));
  const keptByCompose = composeLoadedExchanges([], [echo], false);
  assert.equal(
    keptByDrop.exchanges.includes(echo),
    keptByCompose.includes(echo),
    "同一条 seq=0 用户行：dropTransientExchanges 保留它，而 composeLoadedExchanges(inFlight=false) 丢弃它 —— 两个机制判法相反",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】⑨ compact 边界规则会丢掉「本轮中途 compact」时本轮的早期行
//
// 该规则的前提是假的：`composeLoadedExchanges` 的注释称「compact 是服务端历史的重置点，
// 在它之前产生的瞬时行属于服务端已经丢掉的那段历史」—— 而服务端交换 JSONL 是
// **append-only**：全仓唯一的 `os.Remove` 在两处 `DeleteSession`（`manager.go:1439/:1447`），
// compact 在服务端只写一条 `CompactNotice` aux（`usecase/session.go:2652`）。一行都没丢。
//
// 于是「本轮跑到一半发生了 compact」时：本轮 compact **之前**那些瞬时行被无条件丢弃，
// 而它们在服务端**没有任何替代品**（本轮的助手行要到回合末才落盘）⇒ 用户在切回/重锚定时
// 看到「本轮前半段消失」。这与 `858c3e7` 修的「切回运行中会话塌掉」是同一族。
//
// 注：删这条规则要同时确认「跨多次 compact 堆积 1100+ 条」不会回归 —— 那一族的正解是
// 「不在跑就没在途内容」（主清理），边界规则只是它的替代品。第三步落地时必须两条一起验。
test("【红·⑨b】本轮中途 compact 时，本轮此前的在途行不得被丢弃", () => {
  const early = ex("agent", "本轮前半（compact 之前产生）");
  const notice = ex("compact", "compact");
  const late = ex("agent", "本轮后半");
  const out = composeLoadedExchanges([], [early, notice, late], true);
  assert.deepEqual(
    out.map((e) => e.content),
    ["本轮前半（compact 之前产生）", "compact", "本轮后半"],
    "本轮还在跑：compact 之前产生的那些行仍是本轮的可见内容，服务端并没有丢掉它们",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】⑦ aux 合并的幂等性：同一份窗口回包合并两次不得出现两份
//
// `appendExchangeAuxDelta` 是**拼接**语义，而重锚定/重放会**重复投递**同一份窗口回包
// （`restoreActiveSession` 每次加载都会回一次窗口）。拼接语义下第二次投递会把同一张工具卡
// 再加一遍 —— 这正是「工具卡出现两张」的机制之一。另一条路径（`App.tsx` 的
// `{...cache, ...window}`）用的是**按 seq 覆盖**，两者语义相反（冲突⑦）。
test("【红·⑦b】同一份 aux 增量合并两次，不得出现两份", () => {
  const win = { "3": [{ seq: 3, line: 1, toolcall: { callId: "call-1" } }] };
  const once = appendExchangeAuxDelta(undefined, win);
  const twice = appendExchangeAuxDelta(once, win);
  assert.equal(
    twice["3"].length,
    1,
    "同一份窗口回包被合并两次后出现了两份（重锚定会重复投递；拼接语义不幂等）",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// 【当前会红】⑧ 写盘时的 `truncated` 标记必须反映**实际**是否发生了淘汰
//
// 先纠正一处分链路清点的误报：`forceNoTruncate` **不**绕过上限 —— 它只决定写进
// IDB 的 `truncated` 标记（`session.ts:2396`：`truncated: forceNoTruncate ? false : true`），
// 而 `kept` 的切片在两种调用下**完全相同**。
//
// 真正的缺陷因此更尖锐：**数据被砍了，标记却说没砍**。`truncated` 的语义是
// 「下次加载必须全量回源」（`syncSession` 依此决定走全量还是增量），置 false 等于
// 宣布「被砍掉的那段头部永远不用补回来」⇒ 静默丢历史。窗口态正是走这条路径（`:2516`）。
test("【红·⑧b】发生淘汰时 truncated 不得标成 false", () => {
  const many = Array.from({ length: 600 }, (_, i) => persisted("agent", `第 ${i} 行`, i + 1));
  const plain = toPersistentSession(sess(many));
  const windowed = toPersistentSession(sess(many), true);
  // 前提校验：这条用例必须真的触发了淘汰，否则下面的断言毫无意义。
  assert.ok(
    plain.exchanges.length < many.length,
    `判据坏了：${many.length} 行竟然没触发淘汰（上限 ${plain.exchanges.length}）`,
  );
  assert.notEqual(
    windowed.truncated,
    false,
    `被砍到 ${windowed.exchanges.length} 行，却标 truncated=false ⇒ 下次加载不会全量回源，被砍的头部永远不补`,
  );
});


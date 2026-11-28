// 正文块渲染的**行为**测试（CUSTOM(G-BH)，方案 §7）。
//
// 层次：本文件直接 import `src/hooks/sessionBlocks.ts` 的纯函数跑，断言**渲染结果**。
// 与仓库里读源码文本的契约测试是两个层次 —— 那些红了只说明写法变了，这些红了说明
// 用户看到的时间线变了。
//
// 每一条都对应一个具体的坏法：
//   1. 只有 reasoning、没有 text 的轮次渲染成空 —— 这正是本方案要修的那个 bug
//      （实测 os 模型约 15% 的轮次；流式时 thought_chunk 看得见，落库后正文消失）
//   2. 块序不按 pos → 正文与工具卡顺序错乱（后端 BuildTurnBlocks 与旧渲染器逐条同序，
//      前端只要不按 pos 遍历就白对齐了）
//   3. 空 text 块渲染成空气泡
//   4. 未知块类型让渲染炸掉（agent 加新类型时旧前端必须能活）
//   5. context_window 贴错项 → 该轮的 token 徽标消失
//   6. 有块流却仍去读 content → 双写期看不出问题，S2d 删掉 content 后正文全空
import assert from "node:assert/strict";
import { test } from "vitest";

const {
  blockItemID,
  blocksForSeq,
  blocksTextContent,
  buildAssistantTimelineFromBlocks,
  isBlankText,
} = await import("../src/hooks/sessionBlocks.ts");
const { timelineItemSpacing } = await import(
  "../src/components/session/sessionViewerFormat.ts"
);

const ex = (over = {}) => ({ seq: 7, agent: "claude", timestamp: "2026-10-10T00:00:00Z", ...over });
const block = (pos, type, over = {}) => ({ seq: 7, pos, type, ...over });

test("只有 reasoning 的轮次必须渲染出内容（本 bug 的核心回归）", () => {
  // 用户看到的坏法：流式时思考过程滚动可见，回复结束后整轮**消失**。
  const items = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "reasoning", { text: "我想了一下" }),
  ]);
  assert.equal(items.length, 1, "只有 reasoning 的轮次不得渲染成空");
  assert.equal(items[0].type, "thought");
  assert.equal(items[0].content, "我想了一下");
});

test("按 pos 遍历，reasoning 与 text 交错保持块序", () => {
  const items = buildAssistantTimelineFromBlocks(ex(), [
    block(2, "text", { text: "第二段" }),
    block(0, "reasoning", { text: "先想" }),
    block(1, "text", { text: "第一段" }),
    block(3, "reasoning", { text: "再想" }),
  ]);
  assert.deepEqual(
    items.map((item) => item.type),
    ["thought", "assistant_text", "assistant_text", "thought"],
  );
  assert.deepEqual(
    items.map((item) => (item.type === "thought" ? item.content : item.content)),
    ["先想", "第一段", "第二段", "再想"],
  );
});

test("工具卡 / todo / plan / compact 都按 type 渲染成对应项", () => {
  const items = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "text", { text: "开工" }),
    block(1, "tool_call", {
      toolcall: { callId: "c1", kind: "edit", status: "completed", title: "edit" },
    }),
    block(2, "todo", { todo: { items: [] } }),
    block(3, "plan", { plan: { id: "p1", content: "计划" } }),
    block(4, "compact", { compact: { id: "k1" } }),
    block(5, "text", { text: "收工" }),
  ]);
  assert.deepEqual(
    items.map((item) => item.type),
    ["assistant_text", "tool", "todo", "plan", "compact", "assistant_text"],
  );
  const tool = items[1];
  assert.equal(tool.toolCall.callId, "c1");
  // 工具状态必须归一化：后端可能给 completed，前端只认 complete。
  assert.equal(tool.toolCall.status, "complete");
});

test("空 text / 空 reasoning 块不产出空气泡", () => {
  const items = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "text", { text: "" }),
    block(1, "reasoning", { text: "" }),
    block(2, "text", { text: "有内容" }),
    block(3, "tool_call", { toolcall: null }),
    block(4, "todo", { todo: null }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].content, "有内容");
});

test("未知块类型跳过而不是抛错", () => {
  const items = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "brand_new_kind", { text: "未来类型" }),
    block(1, "text", { text: "已知" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].content, "已知");
});

test("轮次身份收进 turnMeta：工具收尾也不丢，且所有条目共用同一对象", () => {
  // 模型/上下文/缓存不再挂在每轮页脚（常驻状态条负责），这里钉住**轮次身份**：
  // 该轮以工具卡收尾时，工具卡也带着同一份 meta，渲染层才能在该轮最后一行画控件。
  const usage = { inputTokens: 1, outputTokens: 2 };
  const items = buildAssistantTimelineFromBlocks(
    ex({ context_window: { totalTokens: 123, modelContextWindow: 456 }, token_usage: usage }),
    [
      block(0, "text", { text: "一" }),
      block(1, "reasoning", { text: "想" }),
      block(2, "text", { text: "二" }),
      block(3, "tool_call", { toolcall: { callId: "c1", kind: "read" } }),
    ],
  );
  assert.equal(items.at(-1).type, "tool", "这轮以工具卡收尾");
  for (const item of items) {
    assert.equal(item.turnMeta.turnKey, "s7", "持久轮次按 seq 认轮");
    assert.equal(item.turnMeta.completed, true, "服务端已持久化 = 这一轮已完整结束");
    assert.equal(item.turnMeta, items[0].turnMeta, "同一轮共用一个 meta 对象（分组/去重靠身份）");
  }
  assert.equal(items.at(-1).turnMeta.content, "一\n二", "控件行复制的是本轮正文");
});

test("瞬时轮次按传入位置区分轮次身份，且不得标成已完成", () => {
  const first = buildAssistantTimelineFromBlocks(
    ex({ seq: 0, timestamp: "2026-11-15T00:00:00Z" }),
    [block(0, "text", { text: "第一轮" })],
    "10",
  );
  const second = buildAssistantTimelineFromBlocks(
    ex({ seq: 0, timestamp: "2026-11-15T00:00:01Z" }),
    [block(0, "text", { text: "第二轮" })],
    "11",
  );
  assert.equal(first[0].turnMeta.turnKey, "t10");
  assert.equal(second[0].turnMeta.turnKey, "t11");
  assert.notEqual(first[0].turnMeta.turnKey, second[0].turnMeta.turnKey);
  assert.equal(first[0].turnMeta.completed, false, "运行中的轮次不出现控件");
});

test("整轮没有任何可渲染条目时不再产出纯页脚行", () => {
  // 纯标点 reasoning 被过滤后，控件行没有锚点；模型/上下文由常驻状态条承载。
  // 旧实现造的 turn_meta 假行正是「同步后底部刷出一堆控件」与跨轮边界的来源。
  const items = buildAssistantTimelineFromBlocks(
    ex({ model: "ok", model_display_name: "ok" }),
    [block(0, "reasoning", { text: "..." })],
  );
  assert.deepEqual(items, []);
});

test("工具分组保留轮次身份，且绝不跨轮合并", async () => {
  const { groupConsecutiveToolCalls } = await import("../src/hooks/sessionTimeline.ts");
  const { collectCompletedTurnEndIndices } = await import(
    "../src/components/session/sessionViewerFormat.ts"
  );
  const meta = { seq: 7, turnKey: "s7", completed: true, content: "x" };
  const other = { seq: 9, turnKey: "s9", completed: true, content: "y" };
  const tool = (id, turnMeta) => ({
    id,
    type: "tool",
    toolCall: { callId: id, kind: "read" },
    turnMeta,
  });
  const items = [
    ...Array.from({ length: 5 }, (_, i) => tool(`a${i}`, meta)),
    ...Array.from({ length: 5 }, (_, i) => tool(`b${i}`, other)),
  ];
  const grouped = groupConsecutiveToolCalls(items);
  assert.equal(grouped.length, 2, "两轮各自成组，不得合并成一组");
  assert.equal(grouped[0].turnMeta, meta, "组卡必须保留本轮身份");
  assert.equal(grouped[1].turnMeta, other);
  const ends = collectCompletedTurnEndIndices(grouped);
  assert.equal(ends.get("s7"), 0);
  assert.equal(ends.get("s9"), 1);
});

test("已完成轮次的控件位置与全局流式状态无关", async () => {
  const { collectCompletedTurnEndIndices } = await import(
    "../src/components/session/sessionViewerFormat.ts"
  );
  const done = { seq: 7, turnKey: "s7", completed: true };
  const running = { seq: 0, turnKey: "t1", completed: false };
  const items = [
    { id: "a", type: "assistant_text", content: "完成", turnMeta: done },
    { id: "b", type: "assistant_text", content: "运行中", turnMeta: running },
  ];
  // 旧实现用 `isStreaming && idx === last` 隐藏页脚：轮询翻转时已完成轮次的按钮会
  // 跟着消失/出现。现在只看轮次是否已持久化，运行中新增的行不会影响上一轮。
  const ends = collectCompletedTurnEndIndices(items);
  assert.equal(ends.get("s7"), 0);
  assert.equal(ends.has("t1"), false);
});

test("块 id 用 (seq,pos)，跨 revision 稳定且互不重复", () => {
  assert.equal(blockItemID(7, 3), "block:7:3");
  // 同一块被补正（revision+1）时 id 不变 —— React 与虚拟列表的测量不该重建。
  const first = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "text", { text: "草稿", revision: 0 }),
  ]);
  const second = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "text", { text: "定稿", revision: 1 }),
  ]);
  assert.equal(first[0].id, second[0].id, "revision 变化不得换 id");
  const items = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "text", { text: "a" }),
    block(1, "text", { text: "b" }),
  ]);
  assert.notEqual(items[0].id, items[1].id, "同 seq 不同 pos 必须不同 id");
});

test("blocksForSeq 把空数组当作「没有块流」（判据是非空）", () => {
  assert.equal(blocksForSeq({}, 7), undefined);
  assert.equal(blocksForSeq({ "7": [] }, 7), undefined, "空数组要能走到旧格式回退");
  assert.equal(blocksForSeq({ "7": [block(0, "text", { text: "x" })] }, 7).length, 1);
  assert.equal(blocksForSeq({ "7": [] }, 0), undefined, "seq=0 不是持久行");
});

test("blocksTextContent 只拼 text 块、按 pos 排序", () => {
  assert.equal(
    blocksTextContent([
      block(1, "text", { text: "第二行" }),
      block(0, "reasoning", { text: "思考不算正文" }),
      block(0, "text", { text: "第一行" }),
    ]),
    "第一行\n第二行",
  );
});

// 零宽正文块不得变成时间线条目（2026-10-25 用户报「每一个 tool call 之间间距隔得非常大」）。
//
// 形态：agent 在两个工具调用之间吐 `"\n\u200b"` 增量；Go 的 strings.TrimSpace 走
// unicode.IsSpace，U+200B 不在其列，于是它被当成正文落成 text 块。前端只判空串就渲染
// 成一个只有零宽字符的 markdown 段落 —— 段落自带上下的 `p` margin，相邻工具卡的间距
// 从 6px 撑到 87.5px（真机截图里约 110 CSS px），虚拟化的行高估算也跟着错。
test("零宽 text 块不产出条目，工具卡间距保持 6px", () => {
  assert.equal(isBlankText("\u200b"), true);
  assert.equal(isBlankText("\n\u200b"), true);
  assert.equal(isBlankText(" \u200b\u200c\u200d\ufeff "), true);
  assert.equal(isBlankText("有内容\u200b"), false, "真正文里的零宽字符不能连带抹掉");

  const items = buildAssistantTimelineFromBlocks(ex(), [
    block(0, "tool_call", { toolcall: { callId: "c1", kind: "execute", status: "complete" } }),
    block(1, "text", { text: "\u200b" }),
    block(2, "tool_call", { toolcall: { callId: "c2", kind: "execute", status: "complete" } }),
    block(3, "text", { text: "\n\u200b" }),
    block(4, "tool_call", { toolcall: { callId: "c3", kind: "execute", status: "complete" } }),
  ]);
  assert.deepEqual(
    items.map((item) => item.type),
    ["tool", "tool", "tool"],
    "零宽 text 块不得产出 assistant_text（否则间距与行高都被它撑开）",
  );
  // 间距判据：工具卡接工具卡 → 6px。中间夹一个 assistant_text 会变成两条 16px。
  for (let i = 1; i < items.length; i += 1) {
    assert.equal(timelineItemSpacing(items[i - 1], items[i]), "6px");
  }
  // 正文取文本的路径同样不能把零宽块拼进去（否则 user 行/摘要会带上不可见字符）。
  assert.equal(
    blocksTextContent([
      block(0, "text", { text: "第一行" }),
      block(1, "text", { text: "\u200b" }),
      block(2, "text", { text: "第三行" }),
    ]),
    "第一行\n第三行",
  );
});

// 2026-11-28：控件行移出时间线、统一到底部左侧槽位后，不再需要按上一行类型调外边距，
// 2026-11-16 的「压缩块吞掉页脚」也不会再发生（页脚不在正文流里）。这里只保留控件
// 自身的渲染契约：调用方给的外边距必须生效。
test("SessionViewerTurnActions 使用调用方给的控件行外边距", async () => {
  const React = (await import("react")).default;
  const { renderToString } = await import("react-dom/server");
  const { SessionViewerTurnActions } = await import(
    "../src/components/session/SessionViewerTurnActions.tsx"
  );
  const html = renderToString(
    React.createElement(SessionViewerTurnActions, {
      time: "18:36",
      durationLabel: "",
      copied: false,
      t: (key) => key,
      themeColor: "#000",
      userMetaButtonStyle: {},
      marginTop: "0",
      onCopy: () => {},
    }),
  );
  assert.match(html, /data-testid="turn-actions"/);
  assert.match(html, /margin-top:0/, "控件行必须按传入外边距渲染");
});

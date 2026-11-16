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

test("context_window/usage 收进整轮页脚数据：工具收尾也不丢（2026-11-15 改）", () => {
  // 旧行为是贴到最后一个 assistant_text。工具卡/思考卡收尾的轮次没有「最后一段正文」
  // 可挂，页脚整轮消失；纯标点 reasoning 被过滤后更是整轮无行。现在数据挂到每个条目
  // 的 turnMeta 上，渲染时只在该轮最后一行画。
  const cw = { totalTokens: 123, modelContextWindow: 456 };
  const usage = { inputTokens: 1, outputTokens: 2 };
  const items = buildAssistantTimelineFromBlocks(
    ex({ context_window: cw, token_usage: usage }),
    [
      block(0, "text", { text: "一" }),
      block(1, "reasoning", { text: "想" }),
      block(2, "text", { text: "二" }),
      block(3, "tool_call", { toolcall: { callId: "c1", kind: "read" } }),
    ],
  );
  assert.equal(items.at(-1).type, "tool", "这轮以工具卡收尾");
  for (const item of items) {
    assert.deepEqual(item.turnMeta.contextWindow, cw, "每个条目都要能取到本轮页脚");
    assert.deepEqual(item.turnMeta.tokenUsage, usage);
  }
  assert.equal(items.at(-1).turnMeta.content, "一\n二", "页脚里带本轮正文（复制按钮用）");
});

test("整轮没有可渲染条目时仍产出纯页脚行（模型/用量不丢）", () => {
  // 纯标点 reasoning 被过滤，整轮没有任何条目 —— 旧实现会让这一轮彻底消失，
  // 用户既看不到模型/用量，也不知道这轮发生过。
  const cw = { totalTokens: 10, modelContextWindow: 200000 };
  const items = buildAssistantTimelineFromBlocks(
    ex({ model: "ok", model_display_name: "ok", context_window: cw }),
    [block(0, "reasoning", { text: "..." })],
  );
  assert.equal(items.length, 1);
  assert.equal(items[0].type, "turn_meta");
  assert.equal(items[0].turnMeta.modelDisplayName, "ok");
  assert.deepEqual(items[0].turnMeta.contextWindow, cw);
  assert.equal(items[0].id, "block:7:-1", "纯页脚行 id 不得与真实块 id 冲突");
});

test("瞬时轮次的纯页脚行 id 用传入身份，不再全撞 block:0:-1", () => {
  // 流式期 exchange 还没有 seq（或 seq=0）。两轮都只产出纯页脚时，
  // blockItemID(0,-1) 会让所有瞬时轮次拿到同一个 id —— React/virtual key 重复，
  // 行会定位到同一个 start。调用方已经算好了全数组位置的 idToken，必须用它。
  const first = buildAssistantTimelineFromBlocks(
    ex({ seq: 0, timestamp: "2026-11-15T00:00:00Z" }),
    [block(0, "reasoning", { text: "..." })],
    "10",
  );
  const second = buildAssistantTimelineFromBlocks(
    ex({ seq: 0, timestamp: "2026-11-15T00:00:01Z" }),
    [block(0, "reasoning", { text: "..." })],
    "11",
  );
  assert.equal(first[0].type, "turn_meta");
  assert.equal(second[0].type, "turn_meta");
  assert.notEqual(first[0].id, second[0].id, "瞬时纯页脚行必须按 idToken 区分");
});

test("turn_meta 是跨轮边界：上一轮页脚不能被下一轮的纯页脚行吞掉", async () => {
  // 一轮以 assistant_text 收尾、下一轮没有可渲染条目时，next.type 是 turn_meta。
  // 旧判据只认 user_text，于是上一轮拿不到 isAssistantFlowEnd，页脚整轮消失。
  const { isAssistantFlowEnd } = await import(
    "../src/components/session/sessionViewerFormat.ts"
  );
  assert.equal(typeof isAssistantFlowEnd, "function");
  assert.equal(
    isAssistantFlowEnd({ type: "assistant_text" }, { type: "turn_meta" }),
    true,
    "下一行是 turn_meta 时，上一轮也必须画页脚",
  );
  assert.equal(
    isAssistantFlowEnd({ type: "assistant_text" }, { type: "assistant_text" }),
    false,
  );
  assert.equal(
    isAssistantFlowEnd({ type: "assistant_text" }, { type: "user_text" }),
    true,
  );
  assert.equal(isAssistantFlowEnd({ type: "assistant_text" }, null), true);
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

// 整轮页脚跟在一张卡片后面时不得用负外边距。
//
// 2026-11-16 真机（手机截图 1080×2374）：/compact 之后那一轮的页脚（复制/fork 图标、
// 模型、用量、`78%-used(782K/4M)`）被压进压缩块底部，卡片下边框从页脚中间穿过。
// 页脚上外边距原本恒为 -10px，是按「上一行是正文段落」调的；工具卡/组卡/待办/计划/
// 压缩块都是自带边框与 10px 内边距的卡片，负外边距必然把页脚拽进卡片里。
test("卡片收尾时整轮页脚不压进卡片（真机：压缩块吞掉页脚）", async () => {
  const { turnFooterMarginTop } = await import(
    "../src/components/session/sessionViewerFormat.ts"
  );
  assert.equal(typeof turnFooterMarginTop, "function");
  // 正文是 markdown，段落自带 ~10px 下边距 —— 负外边距正好收紧。
  assert.equal(turnFooterMarginTop({ type: "assistant_text" }), "-10px");
  // 思考块也是卡片（ThinkingBlock：1px 边框 + 折叠头 padding），不能吃负外边距。
  for (const type of ["thought", "tool", "tool_group", "todo", "plan", "compact"]) {
    assert.equal(
      turnFooterMarginTop({ type }),
      "4px",
      `${type} 收尾时页脚必须在卡片下方，不能压在卡片上`,
    );
  }
});

test("SessionViewerTurnMeta 使用调用方给的页脚外边距", async () => {
  const React = (await import("react")).default;
  const { renderToString } = await import("react-dom/server");
  const { SessionViewerTurnMeta } = await import(
    "../src/components/session/SessionViewerTurnMeta.tsx"
  );
  const html = renderToString(
    React.createElement(SessionViewerTurnMeta, {
      meta: { timestamp: "2026-11-16T10:00:00Z", agent: "claude", content: "x" },
      time: "18:36",
      durationLabel: "",
      exchangeMeta: "",
      copied: false,
      t: (key) => key,
      themeColor: "#000",
      userMetaButtonStyle: {},
      marginTop: "4px",
      onCopy: () => {},
    }),
  );
  assert.match(html, /data-testid="turn-meta"/);
  assert.match(html, /margin-top:4px/, "页脚必须按传入外边距渲染，卡片收尾时才不会重叠");
});

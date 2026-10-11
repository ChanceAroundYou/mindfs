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
import { register } from "node:module";
import assert from "node:assert/strict";
import test from "node:test";

register("./ts-module-hook.mjs", import.meta.url);

const {
  blockItemID,
  blocksForSeq,
  blocksTextContent,
  buildAssistantTimelineFromBlocks,
} = await import("../src/hooks/sessionBlocks.ts");

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

test("context_window 贴到最后一个 assistant_text（与旧渲染器同一取舍）", () => {
  const cw = { totalTokens: 123, modelContextWindow: 456 };
  const items = buildAssistantTimelineFromBlocks(
    ex({ context_window: cw, token_usage: { inputTokens: 1, outputTokens: 2 } }),
    [
      block(0, "text", { text: "一" }),
      block(1, "reasoning", { text: "想" }),
      block(2, "text", { text: "二" }),
      block(3, "tool_call", { toolcall: { callId: "c1", kind: "read" } }),
    ],
  );
  assert.equal(items[0].contextWindow, undefined, "不能贴到第一段");
  assert.deepEqual(items[2].contextWindow, cw, "必须贴到最后一个 assistant_text");
  assert.deepEqual(items[2].tokenUsage, { inputTokens: 1, outputTokens: 2 });
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

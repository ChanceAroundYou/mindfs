// 无内容思考占位（CUSTOM(G-LR)）：已从块流路径扩到流式与 aux 回退三条路径。
//
// 实测（2026-11-25，pc-wsl / CMAI）：上游 redacted thinking 落盘就是
// `{"thinking":"...","type":"thinking","signature":"..."}`，MindFS 原样导入成
// reasoning 块与 aux thought。用户看到「思考过程 (3 字符)」，展开只有一个 `...`。
// G-LR 当初只过滤了块流一条路径，另外两条照样把它画出来：
//   - 流式瞬时行（回合进行中/刚结束，App 缓存里还是 role:"thought" 行）
//   - aux 回退（导入型/老会话；窗口去重不作用于增量同步的 exchange_aux）
// 这里直接 import 纯函数断言**渲染结果**，与读源码的契约测试分层。
import assert from "node:assert/strict";
import { test } from "vitest";

const { isPlaceholderThought, isBlankText, hasMeaningfulChar } = await import(
  "../src/shared/thoughtText.ts"
);
const { buildTimelineItems } = await import("../src/hooks/sessionTimeline.ts");
const { buildAssistantTimeline } = await import(
  "../src/hooks/sessionAssistantTimeline.ts"
);
const { buildAssistantTimelineFromBlocks } = await import(
  "../src/hooks/sessionBlocks.ts"
);

const T0 = "2026-11-25T04:19:00Z";

test("占位判据：空白/零宽/纯标点算占位，真实思考不算", () => {
  for (const placeholder of ["...", "…", "。。。", " ", "", "\u200b", "---", "(…)"]) {
    assert.equal(isPlaceholderThought(placeholder), true, `应判为占位: ${JSON.stringify(placeholder)}`);
  }
  // 真实思考至少有一个字母/数字/CJK —— 哪怕开头就是省略号也不能误杀。
  for (const real of ["先想一下", "Let me check", "…所以先读文件", "step 2", "结论：3 处"]) {
    assert.equal(isPlaceholderThought(real), false, `不应判为占位: ${JSON.stringify(real)}`);
  }
  // 两个底层判据仍各自可用（isBlankText 不连带抹掉真正文里的零宽字符）。
  assert.equal(isBlankText("\n\u200b"), true);
  assert.equal(hasMeaningfulChar("…所以"), true);
});

test("块流路径：只有占位 reasoning 的轮次不产出任何条目", () => {
  // 控件行没有正文可复制、也没有锚点；模型/上下文由常驻状态条承载，
  // 不再为「这一轮发生过」造一个只有页脚的假行。
  const items = buildAssistantTimelineFromBlocks(
    { seq: 7, agent: "claude", timestamp: T0, model: "opus5.5" },
    [{ seq: 7, pos: 0, type: "reasoning", text: "..." }],
    "s7",
  );
  assert.deepEqual(items, [], "占位既不能产出思考条目，也不产出纯页脚行");
});

test("块流路径：真实 reasoning 照常渲染", () => {
  const items = buildAssistantTimelineFromBlocks(
    { seq: 8, agent: "claude", timestamp: T0 },
    [{ seq: 8, pos: 0, type: "reasoning", text: "先看清结构再改" }],
    "s8",
  );
  assert.equal(items[0].type, "thought");
  assert.equal(items[0].content, "先看清结构再改");
});

test("流式瞬时行：role=thought 的占位不入时间线", () => {
  const exchanges = [
    { seq: 0, role: "user", content: "hi", timestamp: T0, agent: "claude" },
    { seq: 0, role: "thought", content: "...", thought_id: "th-1", timestamp: T0, agent: "claude" },
    { seq: 0, role: "assistant", content: "好的", timestamp: T0, agent: "claude" },
  ];
  const items = buildTimelineItems(exchanges, {}, {}, 0);
  assert.equal(
    items.some((item) => item.type === "thought"),
    false,
    "流式占位思考不能画成「思考过程 (3 字符)」",
  );
  assert.ok(items.some((item) => item.type === "assistant_text"));
});

test("流式瞬时行：真实 thought 保留身份（thought_id 不变）", () => {
  const exchanges = [
    {
      seq: 0,
      role: "thought",
      content: "让我先读一下文件",
      thought_id: "th-real",
      timestamp: T0,
      agent: "claude",
    },
  ];
  const items = buildTimelineItems(exchanges, {}, {}, 0);
  assert.equal(items.length, 1);
  assert.equal(items[0].type, "thought");
  assert.equal(items[0].id, "th-real");
});

test("aux 回退路径：占位 thought 不渲染，plan 不被吞掉", () => {
  // 同一条 aux 上「thought 占位 + plan 真实」：过滤 thought 后必须继续走 plan 分支，
  // 否则把用户真正需要的计划一起吃掉。
  const auxOnlyPlaceholder = buildAssistantTimeline(
    { seq: 9, content: "", timestamp: T0, agent: "claude" },
    "s9",
    [{ seq: 9, line: 0, thought: "..." }],
  );
  assert.equal(
    auxOnlyPlaceholder.some((item) => item.type === "thought"),
    false,
    "aux 占位思考不能渲染",
  );

  const auxWithPlan = buildAssistantTimeline(
    { seq: 10, content: "", timestamp: T0, agent: "claude" },
    "s10",
    [
      // 同一条 aux 上同时挂着占位 thought 与真实 plan：过滤 thought 后必须继续
      // 走 else if (aux.plan) 分支，否则 plan 跟着一起消失。
      {
        seq: 10,
        line: 0,
        thought: "。。。",
        plan: { content: "先核对日志再改剥离判据" },
      },
    ],
  );
  assert.equal(auxWithPlan.some((item) => item.type === "thought"), false);
  assert.equal(auxWithPlan.some((item) => item.type === "plan"), true, "占位 thought 不能吞掉同轮 plan");
});

test("aux 回退路径：真实 thought 照常渲染", () => {
  const items = buildAssistantTimeline(
    { seq: 11, content: "", timestamp: T0, agent: "claude" },
    "s11",
    [{ seq: 11, line: 0, thought: "先确认 provider 再 fork" }],
  );
  assert.equal(items[0].type, "thought");
  assert.equal(items[0].content, "先确认 provider 再 fork");
});

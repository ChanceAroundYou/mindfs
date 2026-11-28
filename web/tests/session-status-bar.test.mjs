import assert from "node:assert/strict";
import { test } from "vitest";

const { selectSessionStatus } = await import("../src/shared/sessionStatus.ts");

// 会话底部常驻状态条：模型 / 上下文 / 缓存命中。
//
// 2026-11-28 用户要求：运行中也显示、实时更新、永不消失；各 harness 上报字段不同，
// 缺哪项就隐藏哪项，不能显示伪造的 0。
test("状态取最新 agent 行，运行中的瞬时行也算", () => {
  const status = selectSessionStatus([
    { role: "user", content: "问题" },
    {
      role: "agent",
      agent: "claude",
      model: "claude-opus-5-5",
      model_display_name: "Opus 5.5",
      effort: "high",
      context_window: { totalTokens: 200_000, modelContextWindow: 1_000_000 },
      token_usage: { inputTokens: 100, outputTokens: 20, cacheReadTokens: 90 },
    },
    { role: "agent", agent: "claude", model: "claude-sonnet-5", model_display_name: "Sonnet 5" },
  ]);
  assert.equal(status.agent, "claude");
  assert.equal(status.model.model, "claude-sonnet-5", "模型取最近一次有值的行（运行中实时前进）");
  assert.equal(status.model.modelDisplayName, "Sonnet 5");
  assert.deepEqual(
    status.contextWindow,
    { totalTokens: 200_000, modelContextWindow: 1_000_000 },
    "新回合还没回填统计时，上一轮的值继续显示（不闪空）",
  );
  assert.equal(status.tokenUsage.cacheReadTokens, 90);
});

test("会话级实时元信息兜底；全缺时每个字段都为空但状态对象仍存在", () => {
  const fromSession = selectSessionStatus([], {
    agent: "codex",
    model: "gpt-5",
    context_window: { totalTokens: 10, modelContextWindow: 200 },
  });
  assert.equal(fromSession.agent, "codex");
  assert.equal(fromSession.model.model, "gpt-5");
  assert.deepEqual(fromSession.contextWindow, { totalTokens: 10, modelContextWindow: 200 });

  const empty = selectSessionStatus(undefined, undefined);
  assert.equal(empty.agent, undefined);
  assert.equal(empty.contextWindow, undefined);
  assert.equal(empty.tokenUsage, undefined);
});

test("上下文窗口只有一半数据时不渲染（不伪造）", () => {
  const status = selectSessionStatus([
    { role: "agent", context_window: { totalTokens: 0, modelContextWindow: 200_000 } },
  ]);
  assert.equal(status.contextWindow, undefined);
});

test("状态条容器常驻，字段按 harness 上报情况逐项显示", async () => {
  const React = (await import("react")).default;
  const { renderToString } = await import("react-dom/server");
  const { SessionViewerStatusBar } = await import(
    "../src/components/session/SessionViewerStatusBar.tsx"
  );
  const render = (exchanges) =>
    renderToString(
      React.createElement(SessionViewerStatusBar, {
        exchanges,
        session: { agent: "claude" },
        agents: [],
        themeColor: "#000",
      }),
    );

  const full = render([
    {
      role: "agent",
      agent: "claude",
      model: "claude-opus-5-5",
      context_window: { totalTokens: 100_000, modelContextWindow: 200_000 },
      token_usage: { inputTokens: 100, outputTokens: 10, cacheReadTokens: 80 },
    },
  ]);
  assert.match(full, /data-testid="session-status-bar"/);
  assert.match(full, /data-testid="status-model"/);
  assert.match(full, /data-testid="status-context"/);
  assert.match(full, /data-testid="status-cache"/);

  const bare = render([{ role: "agent", agent: "claude" }]);
  assert.match(bare, /data-testid="session-status-bar"/, "没有统计时容器也不消失");
  assert.doesNotMatch(bare, /data-testid="status-context"/);
  assert.doesNotMatch(bare, /data-testid="status-cache"/, "harness 没上报缓存就不能画 0%");

  const idle = render([]);
  assert.match(idle, /data-testid="session-status-bar"/, "空会话加载期状态条位置稳定");
});
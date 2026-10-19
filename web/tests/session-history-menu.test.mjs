import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

// 会话标题栏钟表按钮（用户消息摘要）的接线契约。
// 钉的是「结构对不对」，真搬运行为在 useUserMessageSummaries 里验。

test("session-history-menu", async () => {
  const header = readFileSync(new URL("../src/components/session/SessionViewerHeader.tsx", import.meta.url), "utf8");
  const viewer = readFileSync(new URL("../src/components/session/SessionViewer.tsx", import.meta.url), "utf8");
  const history = readFileSync(new URL("../src/components/session/SessionHistoryMenu.tsx", import.meta.url), "utf8");
  const floating = readFileSync(new URL("../src/components/session/SessionViewerFloatingControls.tsx", import.meta.url), "utf8");

  // 钟表按钮在 h1 之后、⋯ 菜单之前（紧贴 ⋯ 左侧）。
  assert.match(header, /\{history \? <SessionHistoryMenu \{\.\.\.history\} \/> : null\}\s*\n\s*\{menu \? <SessionHeaderMenu \{\.\.\.menu\} \/> : null\}/);

  // 只在正式 chat 面板渲染（drawer 不渲染 header，浮球也不再带摘要按钮）。
  assert.match(viewer, /interactionMode === "drawer" \? null : \(/);
  assert.ok(!floating.includes("userMessageSummaries"), "floating controls must no longer render the summary button");

  // 钟表图标 + 计数徽标。
  assert.match(history, /<circle cx="12" cy="12" r="9" \/>/);
  assert.match(history, /M12 7v5l3 2/);
  assert.match(history, /userMessageSummaries\.length > 99/);

  // popup 向下展开，保留 已加载/共 计数行。
  assert.match(history, /top: "calc\(100% \+ 8px\)"/);
  assert.match(history, /已加载/);
  assert.match(history, /windowMeta\.total/);

  // 接线：hover 打开、点击钉住、点击条目跳转。
  assert.ok(history.includes("setUserSummaryHoverOpen(true)"));
  assert.ok(history.includes("setUserSummaryPinnedOpen((open: boolean)"));
  assert.ok(history.includes("scrollToUserMessageSummary(item.index)"));
  assert.ok(history.includes('data-user-summary-index={item.index}'));

  // 文案双语齐备（aria-label / title 仍用 show/hide）。
  const zh = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");
  const en = readFileSync(new URL("../src/i18n/locales/en-US.ts", import.meta.url), "utf8");
  for (const key of ["session.userSummary", "session.showUserSummary", "session.hideUserSummary"]) {
    assert.ok(zh.includes("\"" + key + "\""), `zh-CN missing ${key}`);
    assert.ok(en.includes("\"" + key + "\""), `en-US missing ${key}`);
  }
});

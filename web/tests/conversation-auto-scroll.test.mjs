import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import { panelBottomScrollTop } from "../src/components/task/conversationScroll.ts";

// 打开任务详情时的自动滚动：目标是面板真正的底部，不是对话末尾。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("滚动目标 = 容器底部（scrollHeight - clientHeight）", () => {
  // 内容比可视区高 1400px → 目标就是 1400，与当前滚到哪儿无关。
  assert.equal(panelBottomScrollTop({ scrollHeight: 2000, clientHeight: 600 }), 1400);
  // 幂等：已经到底再算一次仍是同一个值。
  assert.equal(panelBottomScrollTop({ scrollHeight: 2000, clientHeight: 600 }), 1400);
  // 短内容：最大滚动为 0，不做任何滚动。
  assert.equal(panelBottomScrollTop({ scrollHeight: 400, clientHeight: 600 }), 0);
  // 高度相等（恰好铺满）同样不滚。
  assert.equal(panelBottomScrollTop({ scrollHeight: 600, clientHeight: 600 }), 0);
});

test("ConversationView 首屏只滚一次，且不碰两个胶囊的展开状态", () => {
  const view = read("../src/components/task/ConversationView.tsx");
  const helper = read("../src/components/task/conversationScroll.ts");

  assert.match(view, /import \{ alignPanelToBottom, findScrollableAncestor \} from "\.\/conversationScroll"/);
  assert.match(view, /data-testid="conversation-scroll-anchor"/);
  // 以「任务 + 首条活动」为这一轮的身份，重复的 WS 增量不会再次抢滚动。
  assert.match(view, /initialScrollKeyRef/);
  assert.match(view, /const key = `\$\{taskId\}:\$\{activities\[0\]\?\.id \|\| "empty"\}`/);
  assert.match(view, /if \(initialScrollKeyRef\.current === key\) return;/);
  // 布局稳定前重复对齐，但最后一次之后不再继续。
  assert.match(view, /frame < 24 && stable < 3/);
  // 用户自己滚过就让位，不再抢滚动位置。
  assert.match(view, /Math\.abs\(scroller\.scrollTop - lastApplied\) > 2\) return;/);
  // 目标必须是容器底部；对齐对话锚点会把桌面端对话下方的胶囊留到视口外。
  assert.match(view, /alignPanelToBottom\(anchor\);/);
  assert.match(helper, /return Math\.max\(0, input\.scrollHeight - input\.clientHeight\);/);
  assert.doesNotMatch(helper, /anchorBottom|scrollerBottom|getBoundingClientRect/);
  assert.doesNotMatch(view, /alignConversationToBottom|bottomAlignedScrollTop/);
  // 滚动只读几何：不得改胶囊/面板的展开状态。
  assert.doesNotMatch(helper, /setOpenSection|setOpen\(/);
  assert.doesNotMatch(view, /scrollIntoView/);
  // 找不到可滚动祖先时什么都不做，不去滚 window。
  assert.match(helper, /let node: HTMLElement \| null = start\.parentElement;/);
  assert.match(helper, /if \(scrollable && node\.scrollHeight > node\.clientHeight \+ 1\) return node;/);
});

test("零高度元素不吃 flex 间距：间距改为显式 margin", () => {
  const view = read("../src/components/task/ConversationView.tsx");
  const panel = read("../src/components/task/TaskDetailPanel.tsx");

  // 容器不再用 gap：零高度的「回到底部」浮层与滚动锚点会各自白吃一份间距
  // （实测对话 → 输入框 16px 被撑成 32px，输入框 → 胶囊再多 16px）。
  assert.match(
    view,
    /data-testid="conversation-view"\s+style=\{\{ display: "flex", flexDirection: "column", minWidth: 0, maxWidth: "100%" \}\}/,
  );
  assert.doesNotMatch(view, /flexDirection: "column", gap: 16/);
  // 对话 → 输入框的 16px 由输入框外层 margin 提供（含禁用/待回答两种占位）。
  assert.match(view, /marginTop: 16, minWidth: 0/);
  // 滚动锚点保持零高度、无 margin。
  assert.match(
    view,
    /data-testid="conversation-scroll-anchor" aria-hidden style=\{\{ height: 0 \}\}/,
  );
  // 面板内容间距收紧到 6px：输入框 → 执行详情/监管记录胶囊不再留一道死区。
  assert.match(panel, /flexDirection: "column", gap: "6px", minWidth: 0/);
  // 胶囊两端同一顺序，移动端不许再把它提到正文最前面 —— 那样它在手机上就是
  // 一条贴着面板标题下沿的横条（用户复测两次报「吸顶」）。可见性由首屏滚到底
  // 与 useComposerBottomPin 保证，与流内顺序无关。
  assert.doesNotMatch(read("../src/index.css"), /\.task-detail-execution \{\n\s+order: -1;\n\s+\}/);
});

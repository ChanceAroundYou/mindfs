import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";

// 任务详情的滚动结构：对话、输入框、下方胶囊都在同一个 `.mindfs-panel-body` 里，
// 输入框长高不会自动带动滚动位置 —— 多敲一行，下面的胶囊就被顶出视口。
// useComposerBottomPin 负责「输入区长高时把面板底部钉回去」，这里钉住它的判据与
// 接线；真实几何由 Playwright 在桌面/移动两种视口实测。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("输入区长高只在「本来就贴着底部」时重新对齐", () => {
  const src = read("../src/components/task/useComposerBottomPin.ts");

  // 判据只有一个阈值常量，且必须是个小值（贴着底部，不是半屏）。
  assert.match(src, /export const COMPOSER_PIN_PX = (\d+);/);
  const pin = Number(src.match(/export const COMPOSER_PIN_PX = (\d+);/)[1]);
  assert.ok(pin > 0 && pin <= 64, `COMPOSER_PIN_PX=${pin} 太大，会把上翻历史的用户拉回底部`);

  // 高度变化才动；高度没变（宽度变化、字体回流）不许抢滚动位置。
  assert.match(src, /new ResizeObserver\(/);
  assert.match(src, /const delta = height - lastHeight;/);
  assert.match(src, /if \(!delta \|\| !pinned\) return;/);
  // 判定与对齐用的是同一份滚动容器与同一个锚点。
  assert.match(src, /const scroller = findScrollContainer\(anchor\);/);
  assert.match(src, /alignPanelToBottom\(anchor\);/);
  // 用户自己滚动时更新 pinned：上翻历史期间不抢位置。
  assert.match(src, /scroller\.addEventListener\("scroll", onScroll, \{ passive: true \}\)/);
  assert.match(src, /pinned = distanceFromBottom\(scroller\) <= COMPOSER_PIN_PX;/);
  // 卸载时两个监听都要撤掉，否则任务切换后旧输入区还在拽滚动条。
  assert.match(src, /observer\.disconnect\(\);/);
  assert.match(src, /scroller\.removeEventListener\("scroll", onScroll\);/);
  // 没有 ResizeObserver（老浏览器 / SSR）时安静退化成旧行为。
  assert.match(src, /typeof ResizeObserver === "undefined"/);
});

test("详情面板把输入区与滚动锚点接进这个 hook", () => {
  const view = read("../src/components/task/ConversationView.tsx");

  assert.match(view, /import \{ useComposerBottomPin \} from "\.\/useComposerBottomPin";/);
  assert.match(view, /const composerRef = useRef<HTMLDivElement>\(null\);/);
  assert.match(view, /useComposerBottomPin\(composerRef, scrollAnchorRef\);/);
  // 观察对象是输入框外层（含附件条与错误行，它们长高同样要顶住），不是编辑器本身。
  assert.match(view, /<div ref=\{composerRef\} style=\{\{ marginTop: 16, minWidth: 0 \}\}>/);
  // 锚点仍是对话流末尾那个零高度元素：对齐语义与首屏/「回到底部」完全一致。
  assert.match(view, /data-testid="conversation-scroll-anchor"/);
});

test("输入区与胶囊的层级：胶囊排在对话之后，不被输入区盖住", () => {
  const styles = read("../src/index.css");
  // 两端只有一种顺序：对话流（含输入框）→ 胶囊行 → 收尾区。
  // 2026-11-21 曾在移动端用 `order:-1` 把它提到正文最前面，用户复测两次报「吸顶」
  // （它恒在正文顶端、贴着面板标题下沿），那次改动已删除；任何断点都不许再加
  // order / position 覆盖，否则又会变回一条钉在顶上的横条。
  assert.match(styles, /\.task-detail-execution\s*\{\s*min-width: 0;\s*\}/);
  assert.doesNotMatch(styles, /\.task-detail-execution\s*\{[^}]*\border\s*:/);
  assert.doesNotMatch(styles, /\.task-detail-execution\s*\{[^}]*\bposition\s*:/);
});

test("对话面板贴底锚定：卡片向上长，底部胶囊与输入区底边不动", () => {
  const shell = read("../src/components/shell/PanelShell.tsx");
  // PanelShell 提供两种竖直锚点，默认居中（其它弹窗行为不变）。
  assert.match(shell, /align\?: "center" \| "end";/);
  assert.match(shell, /align = "center",/);
  assert.match(shell, /alignItems: align === "end" \? "flex-end" : "center",/);
  // 锚点同时落到 DOM 上，CSS 才能给移动端单独收口。
  assert.match(shell, /data-align=\{align\}/);
  // 移动端遮罩默认 top:36px（顶栏之下），底部锚点会因此把卡片推出屏幕；
  // 贴底面板要改回贴满可视视口。
  assert.match(
    shell,
    /\.mindfs-panel-overlay\[data-align="end"\] \{\s*top: 0 !important;\s*align-items: flex-end !important;/,
  );

  // 只有任务详情走贴底；新建任务面板保持居中（没有「胶囊不能被顶走」的约束）。
  const detail = read("../src/components/task/TaskDetailPanel.tsx");
  assert.match(detail, /<PanelShell[\s\S]{0,200}?align="end"/);
  const create = read("../src/components/app/TaskInlineEditPanel.tsx");
  assert.doesNotMatch(create, /align="end"/);
});

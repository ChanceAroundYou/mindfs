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

test("对话面板固定高度 + 居中：打开第一帧就是最终尺寸，不再贴底向上长", () => {
  const shell = read("../src/components/shell/PanelShell.tsx");
  // 旧的贴底锚点（align="end" + 移动端 flex-end 覆盖）已整体删除，
  // 任务详情改为固定高度、默认居中。
  assert.doesNotMatch(shell, /align\?: "center" \| "end";/);
  assert.doesNotMatch(shell, /data-align=\{align\}/);
  assert.match(shell, /fixedHeight\?: boolean;/);
  assert.match(shell, /fixedHeight = false,/);
  assert.match(shell, /data-fixed-height=\{fixedHeight \? "true" : undefined\}/);
  assert.match(
    shell,
    /height: "min\(88dvh, calc\(var\(--panel-vh, 100dvh\) - 48px\)\)"/,
  );
  assert.match(
    shell,
    /\.mindfs-panel-dialog\[data-fixed-height="true"\] \{\s*height: calc\(var\(--panel-vh, 100dvh\) - 56px\) !important;/,
  );
  // alignItems 只剩居中，没有第二个竖直锚点分支。
  assert.match(shell, /alignItems: "center",/);

  // 双击卡片时第二下会落到刚挂载的遮罩上，面板刚开的一小段窗口内不吃这次点击。
  assert.match(shell, /export const PANEL_OPEN_CLICK_GUARD_MS = 350;/);
  assert.match(shell, /performance\.now\(\) - openedAtRef\.current < PANEL_OPEN_CLICK_GUARD_MS/);

  // 只有任务详情走固定高度；新建任务面板保持内容高度。
  const detail = read("../src/components/task/TaskDetailPanel.tsx");
  assert.match(detail, /<PanelShell[\s\S]{0,220}?fixedHeight/);
  const create = read("../src/components/app/TaskInlineEditPanel.tsx");
  assert.doesNotMatch(create, /fixedHeight/);

  // 固定高度后消息少时要把输入框顶到底部：对话流自身撑满，输入框前留一个
  // 可增长的零内容占位（消息多时收缩到 0，原有的 16px 间距不变）。
  const conversation = read("../src/components/task/ConversationView.tsx");
  assert.match(conversation, /data-testid="conversation-view"[\s\S]{0,180}?flex: "1 0 auto"/);
  assert.match(conversation, /aria-hidden style=\{\{ flex: "1 1 auto", minHeight: 0 \}\}/);
});

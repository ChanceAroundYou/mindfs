import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import { bottomAlignedScrollTop } from "../src/components/task/conversationScroll.ts";

// 打开任务详情时的自动滚动：只做一次几何对齐，不引入跟随/抢滚动。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("锚点底部对齐容器底部；内容不足时滚到 0", () => {
  // 对话末尾在可视区下方 200px → 再滚 200px。
  assert.equal(
    bottomAlignedScrollTop({
      scrollTop: 100,
      scrollHeight: 2000,
      clientHeight: 600,
      anchorBottom: 800,
      scrollerBottom: 600,
    }),
    300,
  );
  // 短内容：最大滚动为 0，不做任何滚动。
  assert.equal(
    bottomAlignedScrollTop({
      scrollTop: 0,
      scrollHeight: 400,
      clientHeight: 600,
      anchorBottom: 500,
      scrollerBottom: 600,
    }),
    0,
  );
  // 锚点已在底部：保持当前位置，不额外下滚。
  assert.equal(
    bottomAlignedScrollTop({
      scrollTop: 1400,
      scrollHeight: 2000,
      clientHeight: 600,
      anchorBottom: 600,
      scrollerBottom: 600,
    }),
    1400,
  );
  // 超过最大滚动 → 夹到 max。
  assert.equal(
    bottomAlignedScrollTop({
      scrollTop: 1390,
      scrollHeight: 2000,
      clientHeight: 600,
      anchorBottom: 900,
      scrollerBottom: 600,
    }),
    1400,
  );
});

test("ConversationView 首屏只滚一次，且不碰两个胶囊的展开状态", () => {
  const view = read("../src/components/task/ConversationView.tsx");
  const helper = read("../src/components/task/conversationScroll.ts");

  assert.match(view, /import \{ alignConversationToBottom, findScrollableAncestor \} from "\.\/conversationScroll"/);
  assert.match(view, /data-testid="conversation-scroll-anchor"/);
  // 以「任务 + 首条活动」为这一轮的身份，重复的 WS 增量不会再次抢滚动。
  assert.match(view, /initialScrollKeyRef/);
  assert.match(view, /const key = `\$\{taskId\}:\$\{activities\[0\]\?\.id \|\| "empty"\}`/);
  assert.match(view, /if \(initialScrollKeyRef\.current === key\) return;/);
  // 布局稳定前重复对齐，但最后一次之后不再继续。
  assert.match(view, /frame < 24 && stable < 3/);
  // 用户自己滚过就让位，不再抢滚动位置。
  assert.match(view, /Math\.abs\(scroller\.scrollTop - lastApplied\) > 2\) return;/);
  // 滚动只读几何：不得改胶囊/面板的展开状态。
  assert.doesNotMatch(helper, /setOpenSection|setOpen\(/);
  assert.doesNotMatch(view, /scrollIntoView/);
  // 找不到可滚动祖先时什么都不做，不去滚 window。
  assert.match(helper, /let node: HTMLElement \| null = start\.parentElement;/);
  assert.match(helper, /if \(scrollable && node\.scrollHeight > node\.clientHeight \+ 1\) return node;/);
});

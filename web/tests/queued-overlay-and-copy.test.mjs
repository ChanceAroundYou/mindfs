import assert from "node:assert/strict";
import { test } from "vitest";
import { readFileSync as fsRead } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => fsRead(join(root, rel), "utf8");

// A/B/C 遮挡与复制三路：红测试。断言“修后”契约，修前必须红。
const queued = read("src/components/action/QueuedMessagesList.tsx");
const actionBar = read("src/components/action/ActionBar.tsx");
const viewer = read("src/components/session/SessionViewer.tsx");
const timeline = read("src/components/session/SessionViewerTimeline.tsx");

function queuedBottomGap() {
  // 外层 padding "0 0 Npx" 的 N，或 marginBottom，取最大值
  let gap = 0;
  const pad = queued.match(/padding:\s*"0 0 (\d+)px"/);
  if (pad) gap = Math.max(gap, Number(pad[1]));
  const mb = queued.match(/marginBottom:\s*"?(\d+)px"?/);
  if (mb) gap = Math.max(gap, Number(mb[1]));
  // ActionBar 内层 column 的 gap（gap: 0 | gap: "8px"）
  const colGap = actionBar.match(/flexDirection:\s*"column",\s*gap:\s*(?:"(\d+)px"|(\d+))/);
  if (colGap) gap = Math.max(gap, Number(colGap[1] ?? colGap[2]));
  return gap;
}

test("A1: queued card bottom gap clears the 28px action buttons", () => {
  // 卡片底到输入框顶 ≥ 一颗按钮高(28px)的可视间隙太贵，最小改动要求 ≥8px，
  // 否则 ✎ 下沿掉进输入条上沿。修前：外层 4px + column gap 0 = 4px。
  assert.ok(
    queuedBottomGap() >= 8,
    `queued bottom gap is ${queuedBottomGap()}px, need >= 8px`,
  );
  assert.match(queued, /minHeight:\s*"28px"/);
  assert.match(queued, /width:\s*"28px",\s*height:\s*"28px"/);
});

test("A2: mobile/desktop queued list keeps the same clearance", () => {
  // 双断：maxHeight mobile116/desktop144 两个分支都在，且间隙不断
  assert.match(queued, /isMobile \? "116px" : "144px"/);
  assert.match(queued, /padding:\s*"0 0 (\d+)px"/);
  assert.ok(
    queuedBottomGap() >= 8,
    `queued bottom gap is ${queuedBottomGap()}px on both breakpoints, need >= 8px`,
  );
});

test("B1: session content bottom stays compact, clearance guard lives in scroll-padding", () => {
  // 内容区与输入条是 AppShell 网格里的两行（`main` / `footer`），本来就不重叠 ——
  // 实测 1500x1000 与 420x900 两个视口滚到底，最后一条与输入条上沿仍有 25px 净空。
  // 所以这里钉的是**两半**：
  //   ① 底留白保持用户要求的 24px（2026-10-23 明确「关联文件与对话框之间太长」，
  //      原值是 composerOverlayInset 派生的 40px）；
  //   ② 真正需要 composerOverlayInset 的是 scroll-padding —— 它管的是
  //      「滚到底 / scrollIntoView」时输入条那一段不被当成可达区域，删了就回到
  //      复制按钮被压在输入条下面的老问题。
  assert.match(viewer, /padding: "24px 16px"/);
  assert.match(viewer, /scrollPaddingBottom: `\$\{composerOverlayInset \+ 20\}px`/);
});

test("C1: turn actions own copy wiring", () => {
  // 2026-11-28：控件行从时间线末尾移到底部左侧槽位（运行中 > 子代理 > 控件）。
  // 复制按钮的接线必须完整地活在 SessionViewerTurnActionsSlot 上，时间线不再持有它。
  const actions = read("src/components/session/SessionViewerTurnActions.tsx");
  const slot = read("src/components/session/SessionViewerTurnActionsSlot.tsx");
  assert.doesNotMatch(timeline, /renderTurnActions/);
  assert.doesNotMatch(timeline, /SessionViewerTurnActions/);
  assert.doesNotMatch(timeline, /lastIndexByTurn/);
  assert.match(slot, /findLatestCompletedTurnIndex/);
  assert.match(slot, /copyText\(meta\.content\)/);
  assert.match(slot, /setCopiedMessageKeys/);
  assert.match(slot, /copyResetTimersRef/);
  assert.match(slot, /reportError\(\s*"clipboard\.write_failed"/);
  assert.match(actions, /onClick=\{onCopy\}/);
  assert.match(actions, /aria-label=\{t\("session\.copyMarkdown"\)\}/);
  assert.match(actions, /aria-label=\{t\("session\.forkFromMessage"\)\}/);
  assert.doesNotMatch(actions, /AgentIcon/, "控件行只保留复制/fork/时间，agent 身份归状态条");
});

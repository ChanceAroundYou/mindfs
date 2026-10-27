import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("tool-view-cache", async () => {
// 工具卡视图缓存（CUSTOM(G-AT)）的源码级守卫。
//
// `daee86b7` 为它写了完整链路（SessionViewer 建缓存 → 时间线透传 →
// ToolCallCard/ToolCallGroupCard 读写），`976a5d0f` 合上游 v0.5.5 时把
// `SessionViewer.tsx` 整体换成了上游版本，`useToolCallCard` 的 `viewCache` 参数从此
// 没有任何调用方 —— **编译通过、测试全绿、类型不报错**，只是虚拟化把工具卡卸载再
// 挂回来时展开态丢失、详情重新请求。这类「代码还在、接线断了」的退化只能钉接线。

const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const viewer = readPhysical("src/components/session/SessionViewer.tsx");
const timeline = readPhysical("src/components/session/SessionViewerTimeline.tsx");
const toolHelpers = readPhysical("src/components/session/sessionViewerToolTimelineHelpers.tsx");
const auxHelpers = readPhysical("src/components/session/sessionViewerAuxiliaryTimelineHelpers.tsx");
const groupCard = readPhysical("src/components/stream/ToolCallGroupCard.tsx");

// ── ① 缓存必须在会话级创建，并一路传到底 ──
assert.match(
  viewer,
  /useMemo<ToolCallViewCache>\(\s*\(\) => \(\{ expanded: new Map\(\), details: new Map\(\) \}\)/,
  "SessionViewer 必须按会话创建 toolViewCache（空 Map 而不是每次渲染新建）",
);
assert.match(
  viewer,
  /toolViewCache=\{toolViewCache\}/,
  "SessionViewer 必须把 toolViewCache 传给时间线 —— 少这一行整条链就是死代码",
);
assert.match(timeline, /toolViewCache\?: ToolCallViewCache/, "时间线必须接受 toolViewCache");
assert.match(
  timeline,
  /renderToolTimelineItem\(\{[^}]*toolViewCache \}\)/,
  "普通工具卡分支必须透传 toolViewCache",
);
assert.match(
  timeline,
  /renderAuxiliaryTimelineItem\(\{[^}]*toolViewCache \}\)/,
  "工具组卡分支必须透传 toolViewCache",
);
assert.match(toolHelpers, /viewCache=\{toolViewCache\}/, "ToolCallCard 必须接上缓存");
assert.match(auxHelpers, /viewCache=\{toolViewCache\}/, "ToolCallGroupCard 必须接上缓存");

// ── 组卡自己的展开态也进缓存：否则展开的组在滚出视口后自己合上（2026-09-28 真机）──
assert.match(
  groupCard,
  /useState\(\(\) => viewCache\?\.expanded\.get\(groupKey\) \?\? false\)/,
  "组卡展开态必须初值取自缓存",
);
assert.match(groupCard, /viewCache\?\.expanded\.set\(groupKey, value\)/, "组卡展开态必须写回缓存");
assert.match(
  groupCard,
  /viewCacheKey=\{`\$\{groupKey\}:\$\{toolCall\.callId \|\| index\}`\}/,
  "子卡键必须挂在组卡键下，否则不同组的同名 callId 会撞键",
);

console.log("tool-view-cache.test.mjs: OK");
});

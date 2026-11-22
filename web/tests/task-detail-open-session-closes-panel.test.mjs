import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("task-detail-open-session-closes-panel", async () => {
// 从任务详情跳进会话后，详情面板要自己关掉。
// 症状：点详情里的「打开会话」（活动卡、执行详情、节点行、工作流会话按钮）后，
// 会话已经在背后切好，面板却还盖在上面，用户看不到刚跳过去的会话。
// 约束：关闭必须发生在导航之后（导航要用当前任务的 root/taskId），
// 且解析失败（空 key / 找不到 root / 没有可跳目标）时不许把面板卸掉。
const root = path.resolve(import.meta.dirname, "..");
const read = (p) => readFileSync(path.join(root, p), "utf8");

// 1) 唯一触发点：抽屉 hook 回报「是否真的发起了跳转」。
const drawer = read("src/app/useAppTaskDrawer.ts");
const drawerBody = drawer.slice(
  drawer.indexOf("const handleTaskSessionDrawerOpen"),
  drawer.indexOf("const refreshTaskRelatedFiles"),
);
assert.ok(drawerBody.length > 0, "handleTaskSessionDrawerOpen must live in useAppTaskDrawer.ts");
assert.match(
  drawerBody,
  /\(sessionKey: string, rootOverride\?: string \| null, taskId\?: string\) => \{/,
  "handleTaskSessionDrawerOpen keeps the (sessionKey, rootOverride, taskId) contract",
);
for (const guard of ["if (!key) return false;", "if (!root) return false;", "if (!target) return false;"]) {
  assert.ok(drawerBody.includes(guard), `missing early exit: ${guard}`);
}
const navigateAt = drawerBody.indexOf("void handleSelectSession(");
const successAt = drawerBody.indexOf("return true;");
assert.ok(navigateAt > 0, "must navigate through handleSelectSession");
assert.ok(successAt > navigateAt, "must report a successful jump only after navigating");
assert.match(
  drawerBody,
  /handleSelectSession\(target as any, \{ preserveTaskSelection: true \}\)/,
  "the jump keeps the board task highlighted (preserveTaskSelection)",
);

// 2) 面板在导航之后关自己，且只在真的跳了才关。
const gate = read("src/components/app/TaskDetailPanelGate.tsx");
assert.match(
  gate,
  /handleTaskSessionDrawerOpen: \(sessionKey: string, rootOverride\?: string \| null \| undefined, taskId\?: string \| undefined\) => boolean;/,
  "the gate prop must surface the boolean result",
);
assert.equal(
  (gate.match(/onOpenSession=\{/g) || []).length,
  1,
  "every detail-panel entry must funnel into one onOpenSession handler",
);
const handlerAt = gate.indexOf("onOpenSession={");
const closeAt = gate.indexOf('if (navigated) setSelectedKanbanTaskId("")');
assert.ok(handlerAt > 0, "the gate must wire onOpenSession");
assert.ok(closeAt > handlerAt, "closing the panel must come after the navigation call");
assert.match(
  gate.slice(handlerAt, closeAt),
  /const navigated = handleTaskSessionDrawerOpen\(\s*sessionKey,\s*selectedKanbanTask\.root_id \|\| currentRootIdRef\.current,\s*selectedKanbanTask\.id,/,
  "navigation must receive the task's own root and id before the panel is closed",
);

// 3) 详情面板内部所有「打开会话」入口继续汇到同一个 onOpenSession（别只改其中一处）。
const panel = read("src/components/task/TaskDetailPanel.tsx");
assert.equal(
  (panel.match(/onOpenSession=\{onOpenSession\}/g) || []).length,
  2,
  "ConversationView and TaskInspectEntries must both receive onOpenSession",
);
const inspect = read("src/components/task/TaskInspectEntries.tsx");
assert.match(inspect, /onOpenSession=\{onOpenSession\}/, "run view must forward onOpenSession");
const runView = read("src/components/task/RunViewEntry.tsx");
assert.match(runView, /onClick=\{\(\) => onOpenSession\(fallbackSessionKey\)\}/, "workflow-session button must use onOpenSession");
const activity = read("src/components/task/ActivityMessage.tsx");
assert.match(activity, /onOpenSession\?\.\(activity\.session_key as string\)/, "activity footer must use onOpenSession");
const nodeRow = read("src/components/task/RunNodeRunRow.tsx");
assert.match(nodeRow, /onOpenSession\(nodeRun\.session_key as string\)/, "node row must use onOpenSession");

console.log("task-detail-open-session-closes-panel.test.mjs ok");
});
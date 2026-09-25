import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 工作台是跨项目视图，两个曾让它「看起来坏掉」的 bug 守在这里：
//   1) 别的项目推送的 task.updated 被丢弃 → 在别处把任务点完成，工作台卡片不变
//   2) 任务详情面板的节点路由写死当前项目 → 跨项目就地编辑打到错节点
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const rt = readFileSync(new URL("../src/app/useRealtimeEvents.ts", import.meta.url), "utf8");

// 1) workspace 模式必须是一个显式的全局状态，而不是「没打开项目」的副产物
assert.match(
  app,
  /const workspaceOpen = mainView === "workspace";/,
  "workspace should be a mode derived from the single main-view state",
);
// 它要被 WS 处理器读到，所以只能走 ref（ref 池与 hook 调用点不在同一层）
assert.match(
  app,
  /const workspaceOpenRef = useRef\(workspaceOpen\);\s*\n\s*workspaceOpenRef\.current = workspaceOpen;/,
  "the realtime handlers read workspace mode through a ref, kept in sync during render",
);
assert.match(
  app,
  /taskDetailsByIdRef,\s*\n\s*workspaceOpenRef,\s*\n\s*\},/,
  "workspaceOpenRef must be handed to useRealtimeEvents in the refs pool",
);

// 2) task.updated：workspace 模式放行任意项目，否则非当前项目的推送被整条丢弃
assert.match(
  rt,
  /"task\.updated":[\s\S]*?workspaceOpenRef\.current \|\| payload\.root_id === currentRootIdRef\.current/,
  "task.updated must pass for any root while the workspace is open",
);
assert.match(
  rt,
  /workspaceOpenRef: RefObject<boolean>;/,
  "the realtime ref pool must declare workspaceOpenRef",
);
// 跨节点隔离仍在：同名项目在另一节点的推送不得污染当前视图
assert.match(
  rt,
  /if \(payloadNid && curNid && payloadNid !== curNid\) \{ return; \}/,
  "cross-node isolation must survive the workspace relaxation",
);

// 3) 任务详情面板的节点路由必须按任务自己的项目解析
assert.match(
  app,
  /nodeId=\{getNodeIdForRoot\(selectedKanbanTask\.root_id \|\| currentRootId \|\| ""\)\}/,
  "TaskDetailPanel must route by the task's own project, not the currently selected one",
);
assert.doesNotMatch(
  app,
  /nodeId=\{currentRootNodeId \|\| undefined\}/,
  "the hardcoded current-project nodeId is the bug being fixed",
);

console.log("workspace-realtime-freshness.test.mjs: OK");

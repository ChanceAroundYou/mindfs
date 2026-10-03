import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 工作台是跨项目视图，两个曾让它「看起来坏掉」的 bug 守在这里：
//   1) 别的项目推送的 task.updated 被丢弃 → 在别处把任务点完成，工作台卡片不变
//   2) 任务详情面板的节点路由写死当前项目 → 跨项目就地编辑打到错节点
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const rt = readFileSync(new URL("../src/app/useRealtimeEvents.ts", import.meta.url), "utf8");
const hook = readFileSync(new URL("../src/app/useWorkspaceBoard.ts", import.meta.url), "utf8");

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

// 4) selectedKanbanTask 必须能回退到 taskDetailsById（2026-10-03 实测：工作台上点卡片
//    面板永远弹不出来）。根因是两份数据没并轨：openWorkspaceTaskDetail 灌的是
//    taskDetailsById，而 kanbanTasks 按 currentRootId 过滤、不含跨项目的任务，
//    面板的渲染门槛读的是 kanbanTasks.find(...) —— 找不到就是 null。
//    上面的守卫只挡住了「把选中清掉」，挡不住「压根没渲染」。
assert.match(
  app,
  /const selectedKanbanTask = useMemo\(\(\) => \{[\s\S]*?kanbanTasks\.find\(\(task\) => task\.id === selectedKanbanTaskId\)[\s\S]*?return taskDetailsById\[selectedKanbanTaskId\]\?\.task \|\| null;/,
  "selectedKanbanTask must fall back to taskDetailsById so cross-project workbench cards can open the panel",
);
assert.match(
  app,
  /\}, \[kanbanTasks, selectedKanbanTaskId, taskDetailsById\]\);/,
  "the fallback source must be a declared dependency, or React memoizes a stale lookup",
);

// 5) task.deleted 后端一直在发（appcontext.go 的 TaskDeleted），前端必须真的接上：
//    不接的话删掉的任务会一直挂在两块板上，只能靠用户手动重拉。
assert.match(
  rt,
  /"task\.deleted":[\s\S]*?pruneTaskDetails\(/,
  "task.deleted must drop the task from memory instead of leaving it rendered forever",
);
assert.match(
  rt,
  /"task\.deleted":[\s\S]*?if \(workspaceOpenRef\.current\) \{\s*refreshWorkspaceBoard\(\);/,
  "a deleted task leaves the board entirely, which is a structure change only a re-fetch sees",
);

// 6) 清场（task.finish_teardown）会清空 worktree_path，卡片因此可能不该再挂在任何
//    筛选下 —— 同理要重拉结构。
assert.match(
  rt,
  /"task\.finish_teardown":[\s\S]*?if \(workspaceOpenRef\.current\) \{\s*refreshWorkspaceBoard\(\);/,
  "teardown clears worktree_path, so the board structure needs re-fetching too",
);

// 7) 重拉只能挂在「结构真变了」的用户动作与后台收尾上，绝不能挂到 task.updated：
//    那是每条推送扇出一次，多节点下会被事件风暴打爆（与 App.tsx 既有注释同一条纪律）。
const updatedHandler = rt.slice(
  rt.indexOf('"task.updated":'),
  rt.indexOf('"task.finish_teardown":'),
);
assert.doesNotMatch(
  updatedHandler,
  /refreshWorkspaceBoard\(\)/,
  "task.updated is high-frequency; it must update cards in place, never re-fetch the fan-out",
);
// 而卡片「就地更新」这条路径本身必须真的接上了 taskDetailsById，否则点了按钮纹丝不动。
assert.match(
  hook,
  /getLiveTask/,
  "workspace cards must read their live version from taskDetailsById",
);
assert.match(
  app,
  /getLiveTask = useCallback\(\(taskId: string\) => liveTasksById\[taskId\], \[liveTasksById\]\)/,
  "getLiveTask must derive from state, not read a ref: the two useMemos in useWorkspaceBoard only recompute when a dependency changes, and a ref read is invisible to them — the whole freshness path would silently do nothing",
);
assert.match(
  app,
  /const liveTasksById = useMemo\(\(\) => \{[\s\S]*?\}, \[taskDetailsById\]\);/,
  "the live-task map must depend on taskDetailsById so it actually changes when a task updates",
);
assert.match(
  app,
  /getLiveTask,\s*\n\s*getNodeId: getNodeIdForRoot,/,
  "the hook call site must pass getLiveTask through",
);

console.log("workspace-realtime-freshness.test.mjs: OK");

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

// 控制面 vs 数据面的分界：控制面请求必须打页面服务器（controlPath），
// 数据面请求必须继续跟着选中节点走（appPath / appURL + nodeId）。
//
// 这条线一旦模糊，表现出来的 bug 是「选中 pc 之后改偏好，preferences.json
// 写进了 pc 那台机器」——两份配置各写各的，节点表就是这么裂开的。
// 见 docs/multi-node-control-plane.md。

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const control = read("src/services/controlPlane.ts");
const preferences = read("src/services/preferences.ts");
const prompts = read("src/services/prompts.ts");
const webPush = read("src/services/webPush.ts");
const tasks = read("src/services/tasks.ts");
const registry = read("src/services/nodeRegistry.ts");

// controlPath 本身：不接受 nodeId。控制面不属于任何节点，
// 调用方如果想传 nodeId，那说明它要的多半是数据面请求。
assert.match(control, /export function controlPath\(path: string, params\?: URLSearchParams\): string/,
  "controlPath takes a path and query params — and deliberately no nodeId");
assert.match(control, /pageServerPath/, "controlPath must resolve against the page server");
assert.doesNotMatch(control, /appPath\(|appURL\(|getActiveNode\(|getApiBaseURL\(/,
  "controlPath must never resolve through the active node");

// 整份文件都是控制面：一个 appPath 都不能剩。
for (const [name, src] of [["preferences.ts", preferences], ["prompts.ts", prompts], ["webPush.ts", webPush]]) {
  assert.doesNotMatch(src, /\bappPath\(/, `${name} is entirely control plane — no appPath( may remain`);
  assert.doesNotMatch(src, /\bappURL\(/, `${name} is entirely control plane — no appURL( may remain`);
  assert.match(src, /from "\.\/controlPlane"|from "\.\.\/services\/controlPlane"/, `${name} must import controlPath`);
}

// 节点表是控制面：本地那条也要打页面服务器，否则换节点会把节点表写到对面去。
assert.match(registry, /return controlPath\("\/api\/nodes"\);/, "the node table is control plane");
assert.match(registry, /remote: !!item\.remote/, "normalizeRecord must carry the remote flag through");

// tasks.ts 是混的：模板函数是控制面，任务本体是数据面。这是最容易改错的一个文件。
for (const fn of ["fetchTaskTemplates", "saveTaskTemplate", "deleteTaskTemplate", "fetchStageTemplates", "saveStageTemplate", "deleteStageTemplate"]) {
  assert.match(tasks, new RegExp(`export async function ${fn}\\([\\s\\S]{0,400}?controlPath\\(`),
    `${fn} is control plane and must use controlPath`);
}
for (const fn of ["fetchTasksOverview", "fetchTasks", "createTask", "updateTaskInput"]) {
  assert.doesNotMatch(tasks, new RegExp(`export async function ${fn}\\([\\s\\S]{0,400}?controlPath\\(`),
    `${fn} is data plane and must keep following the node`);
}
// 数据面仍要带 nodeId —— 这是 D1「浏览器继续扇出」的载体，改掉了跨节点就没了。
assert.match(tasks, /export async function fetchTasksOverview\([\s\S]{0,300}?nodeId\?: string/,
  "the workspace board must keep fanning out across nodes");

console.log("control-plane-routing.test.mjs: OK");
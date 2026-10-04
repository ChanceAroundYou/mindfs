import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const hook = read("src/app/useTaskTemplates.ts");
const dialog = read("src/components/TaskTemplateDialog.tsx");
const app = read("src/App.tsx");
const runtime = read("src/services/runtime.ts");
const services = read("src/services/tasks.ts");
const registry = read("src/services/nodeRegistry.ts");
const goTypes = fs.readFileSync(path.join(root, "../server/internal/kanban/types.go"), "utf8");
const goStore = fs.readFileSync(path.join(root, "../server/internal/kanban/template_store.go"), "utf8");
const goTest = fs.readFileSync(path.join(root, "../server/internal/kanban/service_test.go"), "utf8");

// 模板路由：只打主节点，按**项目**过滤，不再按 nodeId 路由。
//
// 这条断言 2026-10-04 反转过一次，背景值得留着：原来钉的是「模板请求必须带
// nodeId」，因为本机面板读模板时实际请求了 https://pc.xiaokubao.space/…
// （appURL → basePath → getApiBaseURL(undefined) → getActiveNode() 跟着选中节点走），
// 两边模板 id 和名字都相同，表面完全看不出串了。
//
// 现在反过来：模板库**只有主节点一份**（docs/multi-node-control-plane.md），
// 所以模板请求根本不该看节点是谁——它该问的是「这是哪个项目」，因为模板可以
// 限定项目（TaskTemplate.root_id）。原来那次串号不可能再复发：请求压根不带
// 节点概念，选中哪台机器都打到页面服务器。
assert.match(
  runtime,
  /const active = getActiveNode\(\);\s*\n\s*if \(active\?\.url\) return normalizeExplicitNodeBase\(active\.url\);/,
  "getApiBaseURL(undefined) follows the active node — this is why control-plane calls must not go through it",
);

// 读 / 删都按项目走，不带 nodeId。
assert.match(hook, /fetchTaskTemplates\(currentRootId \|\| undefined\)/, "fetchTaskTemplates must ask by project, not by node");
assert.match(hook, /deleteTaskTemplate\(id\)/, "deleteTaskTemplate must not take a nodeId");
assert.doesNotMatch(hook, /templateNodeId/, "the node-routing helper must be gone");
assert.doesNotMatch(hook, /getNodeIdForRoot/, "the hook must not resolve node ids at all");

// 编辑器保存同样不带节点；它带的是「限定到当前项目」这个作用域。
assert.match(dialog, /currentRootId\?: string \| null;/, "TaskTemplateDialog scopes templates by project");
assert.doesNotMatch(dialog, /nodeId/, "the dialog must not accept a nodeId");
assert.match(dialog, /root_id: scopeToCurrentProject \? \(currentRootId \|\| ""\) : "",/, "saving must persist the project scope");
assert.match(
  app,
  /onSaved=\{handleTaskTemplateSaved\}\s*\n\s*currentRootId=\{currentRootId\}/,
  "App must pass the current project into the template dialog",
);
assert.match(app, /useTaskTemplates\(\{ currentRootId, scopedRootKey \}\)/, "useTaskTemplates no longer needs node resolution");

// 底层签名不再收 nodeId（防止改了调用方却没改实现）。
for (const fn of ["fetchTaskTemplates", "saveTaskTemplate", "deleteTaskTemplate"]) {
  assert.doesNotMatch(services, new RegExp(`export async function ${fn}\\([^)]*nodeId`), `${fn} must no longer accept a nodeId`);
}

// 建任务必须把流水**随包带上**，不能只发 template_id 让后端回查。
// 这是模板离开节点之后唯一能让运行节点建出任务的办法：任务可能建在没有模板库的
// worker 上，那次回查必然报 "task template not found"（kanban/service.go CreateTask）。
// applyStageOverride 没覆盖时返回 undefined，所以必须走 taskStagesForCreate。
assert.match(services, /task_template_name: options\?\.templateName/, "createTask must snapshot the template name for worker nodes");
assert.match(
  services,
  /\.\.\.\(options\?\.stages\?\.length \? \{ stages: options\.stages \} : \{\}\)/,
  "createTask must send the inlined stages",
);
assert.match(app, /const createStages = taskStagesForCreate\(/, "the create-task panel must always resolve stages");
assert.match(app, /templateName: selectedTemplate\.name/, "the create-task panel must send the template name it has in hand");
assert.doesNotMatch(
  app,
  /const overrideStages = applyStageOverride\(/,
  "the old 'no override → omit stages → let the server look up the template' path must be gone",
);

// 后端按项目过滤模板：根模板与该项目的模板都在，别的不在。
assert.match(goTypes, /RootID\s+string\s+`json:"root_id,omitempty"`/, "TaskTemplate must carry a project scope");
assert.match(
  goStore,
  /func \(s \*TemplateStore\) ListTaskTemplatesForRoot\(rootID string\)/,
  "the store must filter templates by project",
);

// 模板数据：user 段统一叫「任务输入」。
// 模板清单随产品调整增减，这里钉住实际存在的名字，而不是把数量写死 ——
// 「优化」模板已下线（task-6 重写模板时移除），写死 3 只会让正常改动误报成回归。
const templates = JSON.parse(read("../task_template.json"));
assert.deepEqual(
  templates.map((tpl) => tpl.name),
  ["debug", "新功能"],
  "bundled template names",
);
for (const tpl of templates) {
  assert.equal(tpl.stages.length, 3, `${tpl.name} must have 3 stages (aligned with the pc node)`);
  assert.equal(tpl.stages[0].snapshot.role, "user", `${tpl.name} stage 0 must be the user stage`);
  assert.equal(tpl.stages[0].snapshot.name, "任务输入", `${tpl.name} stage 0 must be named 任务输入`);
}

console.log("task-template-node-routing.test.mjs: OK");

// 10) active node 兜底不能退到 nodes[0]
//     存的 mindfs_active_node_id 解析不出来时（首次同步前、节点换 id、跨设备带来的
//     旧 id），旧实现退到 nodes[0] —— 那可能正是另一台机器，于是所有不带 nodeId 的
//     请求静默打到 pc。local 节点的 URL 按当前 origin 推导（deviceLocalNodeURL），
//     才是「页面所在这台机器」。
assert.match(
  registry,
  /if \(local\) return local;\s*\n\s*return nodes\[0\] \|\| null;/,
  "getActiveNode must prefer the local node over nodes[0] when the stored active id does not resolve",
);
assert.match(
  registry,
  /const local = nodes\.find\(\(n\) => n\.id === LOCAL_NODE_ID\);/,
  "the fallback must look up the local node by id",
);
// local 节点的 URL 必须由当前 origin 推导，不能用共享列表里持久化的值
assert.match(
  registry,
  /function deviceLocalNodeURL\(\): string \{\s*\n\s*const base = localBaseURL\(\);/,
  "the local node's URL must be derived from the current origin, never read from the shared list",
);
// active id 是纯 localStorage，不参与服务端同步 —— 换设备不会带过来
assert.doesNotMatch(
  registry,
  /enqueueServerWrite\([^\)]*activeId/,
  "the active node id must not be synced to the server (it is per-browser)",
);

// 模板数据：agent 段统一 sonnet
for (const tpl of templates) {
  for (const st of tpl.stages) {
    if (st.snapshot.role === "agent") {
      assert.equal(st.snapshot.model, "sonnet", `${tpl.name}/${st.snapshot.name} must use sonnet`);
    }
  }
}

// 11) max_concurrency 是死字段，已连根拔掉：调度器早已不存在（types.go 注释：
//     「兼容保留：位无调度器时恒为 true」），后端只做了一次 <=0 归一化就没人读。
assert.doesNotMatch(services, /max_concurrency/, "the frontend TaskTemplate type must no longer carry max_concurrency");
assert.doesNotMatch(dialog, /max_concurrency/, "the new-template seed must no longer set max_concurrency");
assert.doesNotMatch(hook, /[Cc]oncurrency/, "the concurrency editor handler must be gone");
assert.doesNotMatch(goTypes, /MaxConcurrency/, "the Go TaskTemplate must no longer carry MaxConcurrency");
assert.doesNotMatch(goStore, /MaxConcurrency/, "the store must no longer normalize MaxConcurrency");
assert.doesNotMatch(goTest, /MaxConcurrency/, "tests must not reference the removed field");
for (const tpl of templates) {
  assert.ok(!("max_concurrency" in tpl), `${tpl.name} must not carry max_concurrency in data`);
}

// 12) active node 跟着选中的项目走
//     之前只有节点切换器会改 active node，于是「选中本机项目 + 上次点过 pc」的
//     组合下，所有不带 nodeId 的请求都发去了 pc。项目本身知道自己在哪个节点
//     （currentRootNodeId / _nodeId），选中它时应该把 active 一起切过去。
assert.match(
  app,
  /currentRootNodeIdRef\.current = nid \|\| null;[\s\S]{0,400}?if \(nid\) \{\s*\n\s*const known = getNodeById\(nid\);\s*\n\s*if \(known && getActiveNodeId\(\) !== nid\) \{\s*\n\s*setActiveNodeId\(nid\);/,
  "selectRootNode must set the active node to the selected project's node",
);
assert.match(
  app,
  /import \{[^}]*getActiveNodeId[^}]*setActiveNodeId[^}]*\} from "\.\/services\/nodeRegistry"/,
  "App must import the active-node setters/getter",
);

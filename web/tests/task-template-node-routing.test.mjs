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

// 回归：模板请求必须带 nodeId。
// 不带的话 appURL → basePath → getApiBaseURL(undefined) → getActiveNode()，
// 请求会打到「当前选中的节点」而不是页面所在那台机器。实测：本机面板
// （127.0.0.1）读模板时实际请求了 https://pc.xiaokubao.space/mindfs/api/task-templates，
// 于是看到的是另一台机器的模板（本机 2 段 / pc 3 段），而两边模板 id 相同、
// 名字也相同，表面上完全看不出串了。
assert.match(
  runtime,
  /const active = getActiveNode\(\);\s*\n\s*if \(active\?\.url\) return normalizeExplicitNodeBase\(active\.url\);/,
  "getApiBaseURL(undefined) follows the active node — this is why unscoped calls leak to another machine",
);

// 三个模板接口（读 / 存 / 删）都必须带 nodeId。
assert.match(hook, /fetchTaskTemplates\(templateNodeId\(\)\)/, "fetchTaskTemplates must carry the current project's nodeId");
assert.match(hook, /saveTaskTemplate\(optimistic, templateNodeId\(\)\)/, "saveTaskTemplate must carry nodeId");
assert.match(hook, /deleteTaskTemplate\(id, templateNodeId\(\)\)/, "deleteTaskTemplate must carry nodeId");
assert.match(
  hook,
  /const templateNodeId = useCallback\(\(\): string \| undefined => \(\s*\n\s*currentRootId \? getNodeIdForRoot\(currentRootId\) : undefined/,
  "templateNodeId must come from the current root's node, like every other task call",
);
assert.match(
  app,
  /useTaskTemplates\(\{ currentRootId, scopedRootKey, getNodeIdForRoot \}\)/,
  "App must pass getNodeIdForRoot into useTaskTemplates",
);

// 编辑器保存那条路同样要带。
assert.match(dialog, /nodeId\?: string;/, "TaskTemplateDialog must accept a nodeId");
assert.match(dialog, /\}, nodeId\);/, "the dialog's saveTaskTemplate must carry nodeId");
assert.match(
  app,
  /onSaved=\{handleTaskTemplateSaved\}\s*\n\s*nodeId=\{currentRootId \? getNodeIdForRoot\(currentRootId\) : undefined\}/,
  "App must pass nodeId to the template dialog",
);

// 底层签名确实收 nodeId（防止改了调用方却没改实现）。
for (const fn of ["fetchTaskTemplates", "saveTaskTemplate", "deleteTaskTemplate"]) {
  assert.match(services, new RegExp(`export async function ${fn}\\([\\s\\S]{0,120}?nodeId\\?: string`), `${fn} must accept nodeId`);
}

// 模板数据：三段、user 段统一叫「任务输入」。
const templates = JSON.parse(read("../task_template.json"));
assert.equal(templates.length, 3, "three bundled templates");
for (const tpl of templates) {
  assert.equal(tpl.stages.length, 3, `${tpl.name} must have 3 stages (aligned with the pc node)`);
  assert.equal(tpl.stages[0].snapshot.role, "user", `${tpl.name} stage 0 must be the user stage`);
  assert.equal(tpl.stages[0].snapshot.name, "任务输入", `${tpl.name} stage 0 must be named 任务输入`);
}
assert.equal(templates.find((t) => t.name === "新功能").max_concurrency, 5, "新功能 keeps max_concurrency 5");

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

import assert from "node:assert/strict";
import fs from "node:fs";

const read = (file) => fs.readFileSync(new URL(`../src/${file}`, import.meta.url), "utf8");
const app = read("App.tsx");
const merge = read("services/sessionListMerge.ts");
const sessionList = read("components/SessionList.tsx");

// 可观测层：右面板刷新的「项目/节点/颜色」三元组日志必须常驻
// A 阶段（日志插桩）断言以下日志点存在；C1 落地后补充 resolveGroupColor 断点。
assert.match(app, /\[session-list\] groups load/);
assert.match(app, /\[session-list\] groups cache-apply/);
assert.match(app, /\[session-list\] groups fetch-done/);
assert.match(app, /\[session-list\] color-missing/);
assert.match(app, /\[managed-roots\] fetch/);
assert.match(app, /\[managed-roots\] refresh/);
assert.match(app, /\[session-list\] boot/);
assert.match(app, /SESSION_LIST_OBS_TAG\s*=\s*"e5"/);
assert.match(app, /\[session-list\] node-switch/);
assert.match(app, /\[node-switch\] flat-reload/);
// 会话分组渲染侧审计（仅 groups 数组变化时打一条，不刷屏）
assert.match(sessionList, /\[session-list\] render[\s\S]*?(fallbackCount|FALLBACK)/);
// C1 后：渲染回退链必须走 resolveGroupColor（不再裸 PALETTE[0]/#2563eb 蓝回退）
assert.match(sessionList, /_nodeColor \|\| resolveGroupColor/);
assert.match(sessionList, /resolveGroupColor\(\s*\{ rootId[\s\S]*_nodeId/);
// 合并覆盖日志：跨节点同 key 互相覆盖时必须可见
assert.match(merge, /\[session-list\] merge override/);

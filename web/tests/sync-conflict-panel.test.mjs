import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import {
  conflictChoiceKeys,
  conflictEditPayload,
  conflictSideSummary,
  groupSyncConflicts,
  parseConflictEdit,
} from "../src/shared/syncConflicts.ts";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const service = read("../src/services/net/controlSync.ts");
const hook = read("../src/hooks/useSyncConflicts.ts");
const panel = read("../src/components/app/SyncConflictPanel.tsx");
const rootView = read("../src/app/AppRootView.tsx");
const zh = read("../src/i18n/locales/zh-CN-workspace.ts");
const en = read("../src/i18n/locales/en-US-workspace.ts");

// 服务端对 users 域只回 hash 与删除标记，不回载荷；这里故意塞一份「万一漏了」的
// 载荷进去，钉住前端即使收到也不展示。prompts 的 key 带 `/`，是最容易被路径吞掉的域。
const usersView = {
  id: "pull:users:u_1",
  domain: "users",
  key: "u_1",
  redacted: true,
  local_hash: "hash-local",
  remote_hash: "hash-remote",
  local: { name: "alice", password_hash: "$2a$10$secret-local" },
  remote: { name: "alice", password_hash: "$2a$10$secret-remote" },
};
const preferencesView = { id: "pull:preferences:agents", domain: "preferences", key: "agents" };

test("按域分组并固定顺序，未知域排最后", () => {
  const groups = groupSyncConflicts([
    { id: "pull:workflows:w1", domain: "workflows", key: "w1" },
    usersView,
    preferencesView,
    { id: "pull:preferences:theme", domain: "preferences", key: "theme" },
    { id: "pull:zeta:k", domain: "zeta", key: "k" },
  ]);
  // 顺序来自 spec §5 的同步域表；未知域不能插队，否则新域会让分组位置随数据漂移。
  assert.deepEqual(groups.map((group) => group.domain), [
    "users",
    "preferences",
    "workflows",
    "zeta",
  ]);
  assert.deepEqual(groups.map((group) => group.conflicts.length), [1, 2, 1, 1]);
  assert.equal(groups[0].label, "syncConflict.domain.users");
  // 未知域没有文案键，交给视图直接显示域名原串。
  assert.equal(groups[3].label, null);
});

test("users 域只给两种裁决，摘要不出现 hash", () => {
  assert.deepEqual(conflictChoiceKeys(usersView), ["local", "primary"]);
  // 不依赖服务端有没有带 redacted 标记：域是 users 就不能出现「手动合并」，
  // 否则用户会把口令 hash 当普通 JSON 编辑。
  assert.deepEqual(conflictChoiceKeys({ ...usersView, redacted: undefined }), ["local", "primary"]);
  assert.deepEqual(conflictChoiceKeys(preferencesView), ["local", "primary", "payload"]);

  const summary = conflictSideSummary(usersView, "local");
  assert.equal(summary.kind, "redacted");
  assert.doesNotMatch(
    JSON.stringify(summary),
    /secret|password_hash|\$2a\$/,
    "users 域摘要泄露了口令 hash",
  );
});

test("两侧摘要区分删除、缺失与载荷，且截断预览", () => {
  assert.equal(conflictSideSummary({ ...preferencesView, remote_deleted: true }, "remote").kind, "deleted");
  assert.equal(conflictSideSummary(preferencesView, "local").kind, "missing");

  const payload = conflictSideSummary({ ...preferencesView, remote: { theme: "dark" } }, "remote");
  assert.equal(payload.kind, "payload");
  assert.match(payload.text, /"theme": "dark"/);

  const big = conflictSideSummary({ ...preferencesView, remote: { blob: "x".repeat(4000) } }, "remote");
  assert.ok(big.text.length <= 601, `载荷预览没有截断：${big.text.length} 字符`);
});

test("读冲突走页面服务器，裁决端点转义 id 并只带需要的字段", () => {
  assert.match(service, /controlPath\("\/api\/control-sync\/conflicts"\)/);
  const listBody = service
    .split("export async function fetchSyncConflicts")[1]
    .split("export async function")[0];
  // 与状态端点同一条规矩：主节点 / 未启用时 404 = 没有这个能力。
  assert.match(listBody, /if \(endpointMissing\(err\)\) return null;/);

  // prompts（<账户>/sha256:<hash>）与 nodes（URL）的 id 自带 `/`，不转义就多出一段路径。
  assert.match(service, /\/api\/control-sync\/conflicts\/\$\{encodeURIComponent\(id\)\}\/resolve/);
  const resolveBody = service
    .split("export async function resolveSyncConflict")[1]
    .split("export async function")[0];
  assert.match(resolveBody, /choice === "payload"/);
  assert.match(resolveBody, /body\.payload = payload/);
  assert.match(resolveBody, /JSON\.stringify\(body\)/);
});

test("批量裁决只在 service 里逐条发送，视图一次提交", () => {
  const batchBody = service
    .split("export async function resolveSyncConflicts")[1]
    .split("export async function")[0];
  assert.match(batchBody, /for \(const conflict of conflicts\)/);
  assert.match(batchBody, /await resolveSyncConflict\(conflict\.id, choice\)/);

  // 视图层不能循环发请求：批量确认只调用一次 resolveMany，重复点击由 busy 挡住。
  // 套件里如果视图也 import service，还会同时踩架构红线。
  assert.doesNotMatch(panel, /\bservices\//);
  assert.equal((panel.match(/resolveMany\(/g) || []).length, 1);
  assert.doesNotMatch(panel, /resolveSyncConflict/);
  assert.match(panel, /disabled=\{busy\}/);
});

test("冲突面板由事件打开，批量裁决必须二次确认且写明条数", () => {
  assert.match(hook, /addEventListener\(CONTROL_CONFLICTS_EVENT/);
  assert.match(hook, /removeEventListener\(CONTROL_CONFLICTS_EVENT/);
  assert.match(hook, /fetchSyncConflicts\(\)/);
  // 确认条里必须带条数：不写数字的「确定要批量裁决吗」等于没让人看清代价。
  assert.match(panel, /syncConflict\.batchConfirm[\s\S]{0,120}count/);
  assert.match(panel, /pendingBatch/);
});

test("App 根视图挂上冲突面板，且只挂一处", () => {
  assert.match(
    rootView,
    /import \{ SyncConflictPanel \} from "\.\.\/components\/app\/SyncConflictPanel"/,
  );
  assert.equal((rootView.match(/<SyncConflictPanel \/>/g) || []).length, 1);
});

test("中英文都补齐冲突面板文案", () => {
  const keys = [
    "syncConflict.title",
    "syncConflict.count",
    "syncConflict.empty",
    "syncConflict.loadFailed",
    "syncConflict.retry",
    "syncConflict.domain.users",
    "syncConflict.domain.preferences",
    "syncConflict.domain.nodes",
    "syncConflict.domain.prompts",
    "syncConflict.domain.pins",
    "syncConflict.domain.workflows",
    "syncConflict.localSide",
    "syncConflict.remoteSide",
    "syncConflict.deleted",
    "syncConflict.passwordChanged",
    "syncConflict.keepLocal",
    "syncConflict.usePrimary",
    "syncConflict.mergeManually",
    "syncConflict.batchKeepLocal",
    "syncConflict.batchUsePrimary",
    "syncConflict.batchConfirm",
    "syncConflict.batchMerge",
    "syncConflict.batchMergeConfirm",
    "syncConflict.submitMerge",
    "syncConflict.payloadInvalid",
    "syncConflict.resolving",
    "syncConflict.resolveFailed",
  ];
  for (const key of keys) {
    const pattern = new RegExp(`"${key.replace(/\./g, "\\.")}"`);
    assert.match(zh, pattern, `中文缺 ${key}`);
    assert.match(en, pattern, `英文缺 ${key}`);
  }
  // 占位符名字也要对：视图传 count，文案写成 {n} 就会原样显示花括号。
  assert.match(zh, /"syncConflict\.batchConfirm":\s*"[^"]*\{count\}/);
  assert.match(en, /"syncConflict\.batchConfirm":\s*"[^"]*\{count\}/);
  assert.match(zh, /"syncConflict\.batchMergeConfirm":\s*"[^"]*\{count\}/);
  assert.match(en, /"syncConflict\.batchMergeConfirm":\s*"[^"]*\{count\}/);
});

test("手动合并初值取完整载荷，删除/脱敏/缺失都不拿提示文字当 JSON", () => {
  const longText = "x".repeat(1200);
  const full = { id: "p1", text: longText };
  assert.equal(conflictEditPayload({ ...preferencesView, remote: full }, "remote"), JSON.stringify(full, null, 2));
  assert.ok(conflictEditPayload({ ...preferencesView, remote: full }, "remote").endsWith(`${longText}"\n}`));
  assert.equal(conflictEditPayload(usersView, "local"), null);
  assert.equal(conflictEditPayload({ ...preferencesView, local_deleted: true }, "local"), null);
  assert.equal(conflictEditPayload(preferencesView, "remote"), null);
  // 远端删除时本地仍有载荷，给得出可编辑的起点；不是把「已删除」当 JSON。
  assert.equal(
    conflictEditPayload({ ...preferencesView, remote_deleted: true, local: full }, "local"),
    JSON.stringify(full, null, 2),
  );
});

test("手动合并提交拒绝空串、非法 JSON 与 null", () => {
  assert.deepEqual(parseConflictEdit("  "), { ok: false, reason: "empty" });
  assert.deepEqual(parseConflictEdit("{oops"), { ok: false, reason: "invalid" });
  assert.deepEqual(parseConflictEdit("null"), { ok: false, reason: "null" });
  assert.deepEqual(parseConflictEdit(' {"a":1} '), { ok: true, payload: { a: 1 } });
});

test("批量自定义合并逐条带各自 payload，部分失败在刷新后仍可见", () => {
  assert.match(service, /export async function resolveConflictPayloadEdits\(/);
  assert.match(service, /resolveSyncConflict\(edit\.id, "payload", edit\.payload\)/);
  assert.match(service, /err\.status === 404 \|\| err\.status === 409/);
  assert.match(hook, /resolveManyPayloads/);
  assert.match(
    hook,
    /resolveManyPayloads[\s\S]{0,500}await reload\(\)[\s\S]{0,120}result\.failed > 0/,
  );
  assert.match(panel, /pendingManualBatch/);
  assert.match(panel, /conflictEditPayload\(/);
  assert.match(panel, /resolveManyPayloads\(edits\)/);
  // 手动编辑器不再拿 600 字符摘要当初值。
  assert.doesNotMatch(panel, /setManual\(\{ id: conflict\.id, text: sideText\(conflict, "remote"\) \}\)/);
});
// 上游裁剪面的「复活检测」：这些文件是我们从上游删掉的（git status=D），
// 合上游时最容易被上游版本静默带回来 —— 而带回来的东西在文件级 diff 里看不出来
// （它只是"没差异了"），所以必须有一条直接看磁盘的测试钉住。
//
// 每组都对应一个具体的坏法：
//   1 被删文件复活       → 能力/UI 回来了，且当年删它们的理由（见 yaml 的 why）没人再看
//   2 悬空引用           → 复活了一半，或本地代码又指回已删模块
//   3 替代实现消失       → 删干净了但替代品丢了，功能整体归零（比复活更糟）
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// 本测试跨出 web/ 看 server/ 与 cli/，故要的是仓库根（web/tests/ 往上三层）
const root = path.resolve(fileURLToPath(import.meta.url), "../../..");

// 1) 上游编排体系（G-Y）：任务组 / 编排调度 / 会话清理，已被「任务自带流水」取代
const REMOVED_ORCHESTRATION = [
  "cli/cmd/task_help.go",
  "cli/cmd/task_operations.go",
  "cli/cmd/task_operations_test.go",
  "server/internal/api/http_task_groups.go",
  "server/internal/api/http_task_orchestration.go",
  "server/internal/api/http_task_orchestration_test.go",
  "server/internal/api/task_group_runner.go",
  "server/internal/kanban/group_messages_test.go",
  "server/internal/kanban/group_reopen_test.go",
  "server/internal/kanban/groups.go",
  "server/internal/kanban/groups_test.go",
  "server/internal/kanban/orchestration.go",
  "server/internal/kanban/orchestration_execution.go",
  "server/internal/kanban/orchestration_store.go",
  "server/internal/kanban/orchestration_test.go",
  "server/internal/kanban/plan.go",
  "server/internal/kanban/recovery.go",
  "server/internal/kanban/session_cleanup.go",
  "server/internal/kanban/session_cleanup_test.go",
  "server/internal/kanban/task_messages.go",
  "server/internal/kanban/task_messages_test.go",
  "server/internal/kanban/task_pagination_test.go",
  "web/src/components/TaskGroupPanel.tsx",
];

// 2) relay 隧道 + e2ee + tokenStation（G-H）：用户 2026-10-08 明确要求**彻底删除**，
//    不是「关掉开关」—— 包、路由、前端入口、proof 头一起没了。
//    这些路径在上游全都还在，合上游时最容易被整份带回来。
const REMOVED_RELAY_E2EE = [
  // 前端入口与服务
  "web/src/components/RelayLocalServicesDialog.tsx",
  "web/src/components/SessionQuickActions.tsx",
  "web/src/services/relayServices.ts",
  "web/src/services/tokenStation.ts",
  "web/src/services/e2ee.ts",
  // 后端包：relay 远程隧道
  "server/internal/relay/credentials.go",
  "server/internal/relay/device.go",
  "server/internal/relay/manager.go",
  "server/internal/relay/service.go",
  "server/internal/relay/service_test.go",
  "server/internal/relay/services.go",
  "server/internal/relay/tips.go",
  "server/internal/relay/wsconn.go",
  // 后端包：端到端加密
  "server/internal/e2ee/config.go",
  "server/internal/e2ee/crypto.go",
  "server/internal/e2ee/manager.go",
  "server/internal/e2ee/manager_test.go",
  // 后端路由
  "server/internal/api/http_relay_services.go",
  "server/internal/api/http_token_station.go",
];

const REMOVED = [...REMOVED_ORCHESTRATION, ...REMOVED_RELAY_E2EE];

for (const rel of REMOVED) {
  assert.ok(!fs.existsSync(path.join(root, rel)), `被删除的上游文件复活了：${rel}`);
}

// 悬空引用：只认 import/require 的模块路径，避免误伤无关同名字段
// （如 agent/config.go 的 tokenStationURL 是 relay 的独立配置，不是被删模块）
const DANGLING = /\b(?:from|require\()\s*["'][^"']*(relayServices|tokenStation|TaskGroupPanel|RelayLocalServicesDialog|SessionQuickActions|internal\/relay|internal\/e2ee|net\/e2ee|services\/e2ee)["']/;

function walk(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === "node_modules" || e.name === "dist" || e.name.startsWith(".")) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.(ts|tsx|js|jsx|mjs|go)$/.test(e.name)) out.push(p);
  }
  return out;
}

for (const p of [...walk(path.join(root, "web/src")), ...walk(path.join(root, "server")), ...walk(path.join(root, "cli"))]) {
  const hit = DANGLING.exec(fs.readFileSync(p, "utf8"));
  assert.ok(!hit, `${path.relative(root, p)} 仍引用已删模块 ${hit?.[1]}`);
}

// 3) 替代实现必须在位。
// 删掉旧编排体系的同时不能把新模型一起丢：任务自带流水快照（Task.Stages/StageTemplate）
// 与 worktree 收尾是替代 orchestration.go / plan.go 的那套东西。
const kanbanTypes = fs.readFileSync(path.join(root, "server/internal/kanban/types.go"), "utf8");
assert.ok(/Stages\s+\[\]StageTemplate/.test(kanbanTypes), "Task.Stages 快照字段丢失");
assert.ok(/type StageTemplate struct/.test(kanbanTypes), "StageTemplate 类型丢失");

const templateStore = fs.readFileSync(path.join(root, "server/internal/kanban/template_store.go"), "utf8");
assert.ok(templateStore.includes("ListStageTemplates"), "阶段模板库（新建任务时的可套用模板）丢失");

const worktreeFinish = fs.readFileSync(path.join(root, "server/internal/kanban/worktree_finish.go"), "utf8");
assert.ok(worktreeFinish.includes("FinishTaskWorktree"), "worktree 收尾（替代上游 recovery/session_cleanup）丢失");

// 上游的 task_groups REST 路由不得被重新注册
const httpSrc = fs.readFileSync(path.join(root, "server/internal/api/http.go"), "utf8");
for (const gone of ["/api/task-groups", "handleTaskGroups", "TaskOrchestration"]) {
  assert.ok(!httpSrc.includes(gone), `http.go 重新挂上了已删的编排端点：${gone}`);
}

// relay / e2ee 的路由与 proof 接线不得被重新注册（G-H，2026-10-08 彻底删除）。
// `protectedEndpoint` 也在名单里：它是上游那层鉴权中间件，G-I 把它整层删了 ——
// 它一旦回来，所有 `r.Get(..., h.protectedEndpoint(...))` 形式的接线会跟着回来。
for (const gone of ["/api/relay", "/api/e2ee", "e2eeHeaderName", "requireWSProof", "protectedEndpoint"]) {
  assert.ok(!httpSrc.includes(gone), `http.go 重新挂上了已删的 relay/e2ee 接线：${gone}`);
}

// 控制面前缀表里不得再出现 relay/e2ee：worker 上它们永远不会有 handler，
// 留着只是两条死路径（写进表里就等于声称「worker 不提供这两个能力」，
// 而真相是「谁都不提供」）。
const roleSrc = fs.readFileSync(path.join(root, "server/internal/nodeinfo/role.go"), "utf8");
for (const gone of ['"/api/relay"', '"/api/e2ee"']) {
  assert.ok(!roleSrc.includes(gone), `nodeinfo 控制面前缀表重新收回了已删端点：${gone}`);
}

// 上游 relay 域名依赖也必须断掉（G-H，2026-10-09 用户拍板「一起砍掉」）：
// 更新检查与下载曾硬编码 relay.a9gent.com —— 隧道删了、这条对上游服务器的外连还在，
// 等于删除只做了一半。缺省必须是「没有这个地址」，只能由构建期环境变量显式提供；
// 两台机器上真正被用到的只有这个字面量，故按整份源码扫（注释里也不许出现）。
for (const [file, why] of [
  ["web/src/services/appUpdate.ts", "又内置了 relay 版本检查地址"],
  ["server/internal/update/service.go", "又内置了 relay 下载回退"],
]) {
  const src = fs.readFileSync(path.join(root, file), "utf8");
  assert.ok(!src.includes("relay.a9gent.com"), `${file} ${why}`);
}

// 死键：入口删了、文案还在。用户能在 i18n 文件里看到「公网访问本地服务」这种
// 早已不存在的功能名，是「删了一半」最典型的残留。
for (const locale of ["zh-CN", "en-US"]) {
  const localeSrc = fs.readFileSync(path.join(root, `web/src/i18n/locales/${locale}.ts`), "utf8");
  assert.ok(
    !localeSrc.includes("fileTree.relayLocalServices"),
    `${locale}.ts 还留着已删入口的死键 fileTree.relayLocalServices`,
  );
}

// relay 时代的「资源别名前缀」也一并清掉（G-H，2026-10-09）：服务端的
// RelayAssetsAlias()、前端的 RELAY_ASSETS_PREFIX、SW 里注入的 RELAY_ALIAS，
// 都只为「relay 会把相对 bundle 改写成 /<前缀>-assets/」这一种形态存在。
// 产生者（服务端 relayed 改写、relay 的 /n/<token>/ 路径）已删，产物里恒为
// <部署前缀>/assets/。**留一半比全留更糟**：两边不一致时排查者会以为别名仍受支持。
for (const [file, gone] of [
  ["internal/deploy/prefix.go", "RelayAssetsAlias"],
  ["server/internal/api/prefix.go", "RelayAssetsAlias"],
  ["web/src/services/net/prefix.ts", "RELAY_ASSETS_PREFIX"],
  ["web/src/main.tsx", "RELAY_ASSETS_PREFIX"],
  ["web/vite.config.ts", "RELAY_ALIAS"],
  // 同一个别名机制的第三块：index.html 里那段「主包加载失败就 alert 版本太老」的兜底。
  // 它的 showNotice() 只在 relay 节点页（/n/<token>/）才放行，而产生这种 URL 的 relay
  // 已经不存在 —— 兜底 100% 触发不了，配合构建期注入的主包正则一起删掉。
  // （本地真出现旧缓存/资源缺失由 services/platform/staleAssetRecovery.ts 收口。）
  ["web/index.html", "MINDFS_MAIN_ASSET_RE"],
  ["web/index.html", "isRelayNodePage"],
  ["web/vite.config.ts", "MINDFS_MAIN_ASSET_RE"],
]) {
  const src = fs.readFileSync(path.join(root, file), "utf8");
  assert.ok(!src.includes(gone), `${file} 又出现了 relay 资源别名接线：${gone}`);
}

console.log(`✓ 上游裁剪面完整：${REMOVED.length} 个被删文件未复活、无悬空引用、替代实现在位`);

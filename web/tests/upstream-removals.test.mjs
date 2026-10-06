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

// 2) 旧 relay / tokenStation 前端入口（G-H）：登录页不该再有它们的痕迹
const REMOVED_RELAY_UI = [
  "web/src/components/RelayLocalServicesDialog.tsx",
  "web/src/components/SessionQuickActions.tsx",
  "web/src/services/relayServices.ts",
  "web/src/services/tokenStation.ts",
];

const REMOVED = [...REMOVED_ORCHESTRATION, ...REMOVED_RELAY_UI];

for (const rel of REMOVED) {
  assert.ok(!fs.existsSync(path.join(root, rel)), `被删除的上游文件复活了：${rel}`);
}

// 悬空引用：只认 import/require 的模块路径，避免误伤无关同名字段
// （如 agent/config.go 的 tokenStationURL 是 relay 的独立配置，不是被删模块）
const DANGLING = /\b(?:from|require\()\s*["'][^"']*(relayServices|tokenStation|TaskGroupPanel|RelayLocalServicesDialog|SessionQuickActions)["']/;

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

console.log(`✓ 上游裁剪面完整：${REMOVED.length} 个被删文件未复活、无悬空引用、替代实现在位`);

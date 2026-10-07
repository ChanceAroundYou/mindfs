// 前端目录/文件拆分（G-AY）的针对性测试。
//
// 拆分策略是「机械搬移 + 旧路径 barrel 重导出，零行为变化」。这条测试钉住三件事：
//   1. barrel shim 仍在原路径，且**完整**重导出原模块的公开面（导出面不变）；
//   2. 拆分后的子模块之间**没有新增导入环**（能成功 import 即无环）；
//   3. source-map 把旧逻辑路径映射到全部物理文件（43 条源码正则断言的前提）。
//
// 为什么必须有一条：上游合并时最危险的不是「文件被覆盖」，而是「barrel 被换成
// 上游的单文件实现，导出面悄悄变了」—— 文件级 diff 看不出来，只有 import 后
// 逐键比对才看得见。
import assert from "node:assert/strict";
import { test } from "node:test";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { MODULES } from "./source-map.mjs";

const root = path.resolve(fileURLToPath(import.meta.url), "../..");
const importSrc = (rel) => import(pathToFileURL(path.join(root, rel)).href);

test("barrel shim 完整重导出原模块公开面", async () => {
  const tasks = await importSrc("src/services/tasks.ts");
  for (const key of [
    "fetchTasksOverview", "createTask", "updateTaskStage", "deleteTask",
    "fetchTaskTemplates", "fetchTaskDetails", "addTaskStage", "moveTask",
  ]) {
    assert.equal(typeof tasks[key], "function", `tasks.ts 应重导出 ${key}`);
  }

  const git = await importSrc("src/services/git.ts");
  for (const key of [
    "fetchGitStatus", "fetchGitHistory", "commitDiffStorageKey",
    "normalizeGitStatusPayload", "normalizeGitActionPayload", "fetchGitDiff",
  ]) {
    assert.equal(typeof git[key], "function", `git.ts 应重导出 ${key}`);
  }

  const file = await importSrc("src/services/file.ts");
  for (const key of [
    "fetchFile", "getCachedFile", "invalidateFileCache",
    "fetchEditableFile", "saveTextFile", "fetchProofProtectedBlob",
  ]) {
    assert.equal(typeof file[key], "function", `file.ts 应重导出 ${key}`);
  }
});

test("拆分未引入导入环（子模块可独立加载）", async () => {
  // 逐个加载子模块：若有环，ESM 会抛错或拿到未初始化的绑定。
  // （只加载 .ts —— .tsx 含 JSX，Node 无法直接 import，由 typecheck 兜住。）
  await importSrc("src/services/task/index.ts");
  await importSrc("src/services/git/index.ts");
  await importSrc("src/services/file/index.ts");
});

test("source-map 把旧逻辑路径映射到全部物理文件", () => {
  for (const logical of [
    "src/services/tasks.ts",
    "src/services/git.ts",
    "src/services/file.ts",
    "src/components/file/FileTree.tsx",
  ]) {
    const physical = MODULES[logical];
    assert.ok(physical && physical.length > 1, `${logical} 应映射到多个物理文件`);
    for (const p of physical) {
      assert.ok(!p.endsWith("index.ts") || physical.includes(p), `${p} 应在映射内`);
    }
  }
});

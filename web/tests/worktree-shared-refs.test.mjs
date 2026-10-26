import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 共享 worktree 的收尾是**只解绑**：不合并、不拆目录、不删分支（2026-10-26 用户定的口径）。
//
// 服务端在引用计数 > 0 时返回 reference_released + remaining_references 并直接返回。
// 界面必须把这件事说出来 —— 否则「目录已拆 / 分支已删」一条都不出现，
// 用户看到的是一个「什么都没发生」的收尾提示，会以为按钮没生效。

test("worktree-shared-refs", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const handler = read("src/app/realtimeWsTaskHandlers.ts");
const types = read("src/services/task/worktree.ts");
const zh = read("src/i18n/locales/zh-CN-tasks.ts");
const en = read("src/i18n/locales/en-US-tasks.ts");

// 契约：服务端新加的两个可选字段，旧前端忽略即可，所以必须可选。
assert.match(types, /reference_released\?:\s*boolean/, "FinishWorktreeResult must carry reference_released");
assert.match(types, /remaining_references\?:\s*number/, "…and remaining_references");

// 结论要能看见：解绑文案必须进 toast 的 lines，且带上剩余数量。
assert.match(
  handler,
  /result\?\.reference_released[\s\S]{0,200}task\.finishWorktreeShared/,
  "the teardown handler must report the unbind case, not stay silent",
);
assert.match(
  handler,
  /remaining_references/,
  "the unbind line must say how many tasks still hold the worktree",
);

for (const [lang, source] of [["zh-CN", zh], ["en-US", en]]) {
  assert.match(source, /"task\.finishWorktreeShared":\s*".*\{count\}.*"/, `${lang} needs the shared-worktree copy with {count}`);
}
});

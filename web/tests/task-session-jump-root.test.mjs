import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

// 工作台卡片「跳会话」的 root 归属（docs/upstream-customizations.yaml 的 G-Z）。
// 症状：第二次点同一张卡片跳到「当前项目」下的同名空白会话（请求打到错误 root → 404）。
// 根因：会话回包不带 root_id，前端把它写进缓存后，二次跳转时归属退化成 currentRoot。
const root = path.resolve(import.meta.dirname, "..");
const sourcePath = path.join(root, "src/app/sessionJump.ts");
const source = readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const { resolveSessionJumpRoot, buildSessionJumpTarget } = sandbox.exports;

const CARD_ROOT = "日程管理";
const OTHER_ROOT = "mindfs";
const KEY = "1791268544-abc";

// 1) 核心回归：卡片声明 root，缓存那份无 root → 必须按卡片 root 走，不能退化成当前根
{
  const cached = { key: KEY, session_key: KEY, name: "功能扩充" };
  const target = buildSessionJumpTarget({
    sessionKey: KEY,
    rootOverride: CARD_ROOT,
    cached,
    currentRoot: OTHER_ROOT,
  });
  assert.equal(target.root_id, CARD_ROOT, "card root must win over current root");
  assert.equal(target.key, KEY);
  assert.equal(target.session_key, KEY);
  assert.equal(target.name, "功能扩充", "cache fields must survive");
}

// 2) 缓存里带错误 root（历史无主缓存的另一种形态）→ 仍以卡片 root 为准；
//    matched 的元数据（name/model）必须补进结果，不能被丢
{
  const cached = { key: KEY, root_id: OTHER_ROOT, exchanges: [{ seq: 1 }] };
  const matched = { key: KEY, root_id: OTHER_ROOT, name: "功能扩充", model: "claude-opus-5" };
  const target = buildSessionJumpTarget({
    sessionKey: KEY,
    rootOverride: CARD_ROOT,
    matched,
    cached,
    currentRoot: OTHER_ROOT,
  });
  assert.equal(target.root_id, CARD_ROOT, "explicit card root overrides a stale cached root");
  assert.equal(target.name, "功能扩充", "matched metadata must fill gaps in the cache");
  assert.equal(target.model, "claude-opus-5");
  assert.deepEqual(target.exchanges, [{ seq: 1 }], "cache payload must be preferred as the base");
}

// 3) 没有显式 root → 退回会话自带 root → 再退回当前根
{
  assert.equal(
    resolveSessionJumpRoot(undefined, { root_id: CARD_ROOT }, OTHER_ROOT),
    CARD_ROOT,
    "session root beats current root",
  );
  assert.equal(
    resolveSessionJumpRoot("", { root_id: CARD_ROOT }, OTHER_ROOT),
    CARD_ROOT,
    "empty explicit root counts as absent",
  );
  assert.equal(
    resolveSessionJumpRoot(null, {}, OTHER_ROOT),
    OTHER_ROOT,
    "current root is the last resort",
  );
}

// 4) key / root 缺失 → null（调用方据此直接 return，不再造出无主会话）
{
  assert.equal(buildSessionJumpTarget({ sessionKey: "", rootOverride: CARD_ROOT }), null);
  assert.equal(buildSessionJumpTarget({ sessionKey: "   ", rootOverride: CARD_ROOT }), null);
  assert.equal(buildSessionJumpTarget({ sessionKey: KEY, currentRoot: "" }), null);
}

// 5) task_id 只在显式传入时写；不传不许被清成空串（缓存里的原值要留着）
{
  const withTask = buildSessionJumpTarget({ sessionKey: KEY, rootOverride: CARD_ROOT, taskId: "task-1" });
  assert.equal(withTask.task_id, "task-1");
  const withoutTask = buildSessionJumpTarget({
    sessionKey: KEY,
    rootOverride: CARD_ROOT,
    cached: { key: KEY, task_id: "task-9" },
  });
  assert.equal(withoutTask.task_id, "task-9");
}

// 6) 接线契约：App.tsx 必须真的用这套 helper、并在三条写缓存的路径上补 root_id。
//    （只测纯函数挡不住「改回原实现」，症状的入口在 App.tsx 这层胶水里。）
const app = readFileSync(path.join(root, "src/App.tsx"), "utf8");
assert.match(
  app,
  /import \{ buildSessionJumpTarget, resolveSessionJumpRoot \} from "\.\/app\/sessionJump";/,
  "App.tsx must import the session-jump helpers",
);
assert.match(
  app,
  /const handleSessionChipClick[\s\S]{0,700}?buildSessionJumpTarget\(\{/,
  "handleSessionChipClick must route through buildSessionJumpTarget",
);
assert.match(
  app,
  /const handleTaskSessionDrawerOpen[\s\S]{0,1200}?buildSessionJumpTarget\(\{/,
  "handleTaskSessionDrawerOpen must route through buildSessionJumpTarget",
);
// 三条缓存写入路径（窗口 / 全量 sync / handleSelectSession）都带 root_id。
// （原先这两条以 `pending,` 作锚点，2026-10-07 pending 改成纯派生、不再写进会话对象，
//   锚点换成紧随其后的字段 —— 钉的契约不变：写缓存必须带 root。）
assert.match(app, /root_id: resolvedRoot,\n\s+_windowMeta: anchoredMeta as any,/g, "window and sync cache writes must stamp root_id");
assert.match(app, /root_id: targetRoot,\n\s*\} as Session;/, "handleSelectSession cache write must stamp root_id");

console.log("task-session-jump-root.test.mjs ok");

import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("session-status-badges", async () => {
// 会话行内容徽标（G-AD）：worktree 标签 + error/plan/todo/task 徽标。
//
// 症状（丢了会怎样）：右侧会话列表只剩名字和时间 —— 有没有 worktree 看不出来，
// plan/todo/task/ask_user 进展和错误提示只有任务卡上有，普通聊天会话的行上一片空白。
// 修法：后端 session.Aux 按会话落盘（三处 payload 共用 AuxFlagsPayload），
// toSessionItem 透传 + session.meta.updated 按 key 增量合并，SessionCard 左侧
// 按钮区渲染 SessionStatusBadges（图标/样式复用任务卡那套，不另起一套）。
//
// 问号脉冲不另设字段：由 aux_flags.ask_user_waiting 驱动；后端从不下发
// awaiting_user，留字段只会养出第二个真相（见 sessionListTypes 注释）。

const normalize = (sourcePath, source) => {
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => {
      if (id.includes("appTask")) return { normalizeFastService: (v) => v };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports;
};

// ── 1. 归一化：唯一入口 normalizeSessionAuxFlags ──
const appSessionPath = path.resolve(import.meta.dirname, "../src/app/appSession.ts");
const appSession = normalize(appSessionPath, fs.readFileSync(appSessionPath, "utf8"));
assert.equal(typeof appSession.normalizeSessionAuxFlags, "function", "appSession must export normalizeSessionAuxFlags");
assert.equal(typeof appSession.toSessionItem, "function", "appSession must export toSessionItem");

const { normalizeSessionAuxFlags, toSessionItem } = appSession;
// vm 沙盒里造出的对象 prototype 属另一个 realm，assert/strict 的 deepEqual
// 会卡原型 —— JSON 过一遍回到主 realm 再比（只关心值）。
const plain = (value) => (value === undefined ? undefined : JSON.parse(JSON.stringify(value)));

// 全空 → undefined（零渲染，不占行高）
assert.equal(normalizeSessionAuxFlags(undefined), undefined, "no payload means no badges");
assert.equal(normalizeSessionAuxFlags(null), undefined, "null payload means no badges");
assert.equal(normalizeSessionAuxFlags({}), undefined, "empty flags means no badges");
assert.equal(
  normalizeSessionAuxFlags({ ask_user_waiting: false, has_plan: false, has_todos: false, has_task: false, session_error: "" }),
  undefined,
  "all-false flags means no badges",
);
assert.equal(normalizeSessionAuxFlags({ session_error: "   " }), undefined, "blank error means no badges");

// 各位置 true 都透出来
assert.deepEqual(
  plain(normalizeSessionAuxFlags({ ask_user_waiting: true })),
  { ask_user_waiting: true, has_plan: false, has_todos: false, has_task: false },
  "ask_user_waiting passes through",
);
assert.deepEqual(
  plain(normalizeSessionAuxFlags({ has_plan: true, has_todos: true, has_task: true, session_error: "boom" })),
  { ask_user_waiting: false, has_plan: true, has_todos: true, has_task: true, session_error: "boom" },
  "plan/todo/task/error pass through",
);

// ── 2. toSessionItem 透传：列表 payload → 行 ──
const item = toSessionItem("root-1", {
  key: "s-1",
  name: "chat",
  type: "chat",
  related_worktree: { root_id: "root-1", path: "/tmp/wt-1" },
  worktree_missing: false,
  aux_flags: { ask_user_waiting: true, has_plan: true, has_todos: false, has_task: false, session_error: "" },
});
assert.ok(item, "toSessionItem must build the item");
assert.equal(item.worktree_missing, false, "worktree_missing passes through");
assert.deepEqual(
  plain(item.aux_flags),
  { ask_user_waiting: true, has_plan: true, has_todos: false, has_task: false },
  "aux_flags passes through (blank error dropped)",
);
const bare = toSessionItem("root-1", { key: "s-2", name: "plain", type: "chat" });
assert.ok(bare, "toSessionItem must build the item without aux payload");
assert.equal(bare.aux_flags, undefined, "no aux payload means no badges, not empty flags");
assert.equal(bare.worktree_missing, undefined, "no worktree payload means no tag state");

// ── 3. WS 增量合并：payload 带才覆盖，不带沿用缓存 ──
const metaPath = path.resolve(import.meta.dirname, "../src/app/realtimeWsSessionMeta.ts");
const meta = normalize(metaPath, fs.readFileSync(metaPath, "utf8"));
assert.equal(typeof meta.createSessionMetaHandlers, "function", "meta module must export createSessionMetaHandlers");
const metaSource = fs.readFileSync(metaPath, "utf8");
assert.match(metaSource, /aux_flags:\s*payload\.session\.aux_flags !== undefined/, "cache merge must overwrite aux_flags only when the payload carries it");
assert.match(metaSource, /worktree_missing:\s*typeof payload\.session\.worktree_missing === "boolean"/, "cache merge must overwrite worktree_missing only when the payload carries a boolean");

// ── 4. 视图接线：SessionCard 左侧渲染徽标条 + 问号由 aux 驱动 ──
// 问号与图标位住在 SessionCardIcon（2026-10-16 从 SessionCard 整块抽出，纯行数门禁），
// 所以断言分两处读：徽标条看 SessionCard，图标位看 SessionCardIcon。
const card = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionCard.tsx"), "utf8");
const icon = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionCardIcon.tsx"), "utf8");
assert.match(card, /<SessionStatusBadges/, "SessionCard must render the badge strip");
assert.match(card, /<SessionWorktreeTag/, "SessionCard must render the green worktree tag");
assert.match(card, /<SessionCardIcon/, "SessionCard must render the icon cell");
assert.match(
  icon,
  /session\.aux_flags\?\.ask_user_waiting/,
  "the question-mark pulse is driven by aux_flags.ask_user_waiting",
);
assert.match(
  icon,
  /renderToolIcon\("ask_user"\)/,
  "the question-mark reuses the task card ask_user icon, not a bespoke SVG",
);
assert.match(
  icon,
  /taskAuxBadgeStyle\(true\)/,
  "the question-mark reuses taskAuxBadgeStyle(true) like the task card attention badge",
);
assert.equal(card.includes("session.awaiting_user"), false, "the orphan awaiting_user field must be gone");
const listTypes = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/sessionListTypes.ts"), "utf8");
assert.equal(listTypes.includes("awaiting_user?:"), false, "the orphan awaiting_user field must be gone from the view type");

// ── 5. 复用边界：不另起图标/样式，错误解析不 import app/ ──
const badges = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionStatusBadges.tsx"), "utf8");
assert.match(badges, /from "\.\.\/task\/taskIcons"/, "badge icons/styles reuse the task card set");
assert.match(badges, /renderToolIcon\("todo"\)/, "todo icon reuses the stream tool icon");
assert.match(badges, /renderToolIcon\("task"\)/, "task icon reuses the stream tool icon");

// ── 6. 绿 worktree 标签：只活树显示 + 悬在名字左上方且不占流（用户 2026-10-16 定）──
const tag = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionWorktreeTag.tsx"), "utf8");
assert.match(tag, /taskWorktreeTagStyle\("enabled"\)/, "the tag reuses the task card worktree tag style");
assert.match(tag, /position: "absolute"/, "the tag must not occupy flow — the name and icons keep their places");
assert.match(tag, /bottom: "100%"/, "the tag hangs above the session name, not below or beside it");
assert.match(tag, /left: 0/, "the tag is left-aligned with the session name");
// 判据只认活树：无 path = 没开过，worktreeMissing = 目录已删（已收尾），两者都不渲染。
assert.match(tag, /Boolean\(path\) && !worktreeMissing/, "only a live worktree (path present, dir not deleted) renders the tag");
assert.equal(tag.includes("NoWorktreeIcon"), false, "no-worktree and finished sessions show nothing at all");
assert.equal(tag.includes("worktreeFinishedLabel"), false, "finished sessions show nothing at all");
assert.equal(badges.includes("NoWorktreeIcon"), false, "the badge strip no longer carries the worktree tag");
assert.equal(badges.includes("worktreeFinishedLabel"), false, "the badge strip no longer carries the worktree tag");
// 徽标条空着就整块不渲染：空的 marginTop 会把只有名字的行顶高。
assert.match(badges, /if \(badges\.length === 0\)/, "an empty badge strip renders nothing");
assert.equal(badges.includes("from \"../../app/"), false, "view layer must not import app/");
assert.equal(badges.includes("services/"), false, "view layer must not import services/");

// ── 7. 徽标尺寸：其他小图标缩到和 worktree 标签一样大（用户 2026-10-16）──
const styles = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/task/taskStyles.ts"), "utf8");
assert.match(
  styles,
  /export function taskAuxBadgeStyle[\s\S]{0,400}?width: "14px"/,
  "the aux badge box shrinks to the 14px worktree tag",
);
assert.match(
  styles,
  /export function taskAuxIconScaleStyle[\s\S]{0,200}?transform: "scale\(0\.75\)"/,
  "16px icons scale down to 12px inside the box",
);
// 缩放只能落在图标外面：attention 的脉冲动画自己就在动 transform（index.css 的
// mindfs-task-ask-user-pulse），写进 taskAuxBadgeStyle 会被动画盖掉、问号跳回原尺寸。
assert.match(badges, /taskAuxIconScaleStyle\(\)/, "the badge strip wraps its icons in the scale");
assert.match(icon, /taskAuxIconScaleStyle\(\)/, "the question-mark badge wraps its icon in the scale");
const taskRows = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/task/TaskCardRows.tsx"), "utf8");
assert.match(taskRows, /taskAuxIconScaleStyle\(\)/, "the task card badge strip wraps its icons in the scale");

// ── 8. 后端三处 payload + 存储列（源码守卫，防静默回退）──
const list = fs.readFileSync(path.resolve(import.meta.dirname, "../../server/internal/api/http_sessions.go"), "utf8");
assert.match(list, /"aux_flags":\s*s\.Aux\.AuxFlagsPayload\(\)/, "session list must carry aux_flags");
const broadcast = fs.readFileSync(path.resolve(import.meta.dirname, "../../server/internal/api/appcontext.go"), "utf8");
assert.match(broadcast, /"aux_flags":\s*sess\.Aux\.AuxFlagsPayload\(\)/, "appcontext broadcast must carry aux_flags");
const ws = fs.readFileSync(path.resolve(import.meta.dirname, "../../server/internal/api/ws.go"), "utf8");
assert.match(ws, /"aux_flags":\s*sess\.Aux\.AuxFlagsPayload\(\)/, "ws broadcast must carry aux_flags");
const schema = fs.readFileSync(path.resolve(import.meta.dirname, "../../server/internal/session/manager_sqlite.go"), "utf8");
for (const col of ["aux_ask_user_waiting", "aux_has_plan", "aux_has_todos", "aux_has_task", "aux_session_error"]) {
  assert.ok(schema.includes(col), `sqlite schema must carry ${col}`);
}
});

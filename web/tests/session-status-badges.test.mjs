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
  worktree_active: true,
  aux_flags: { ask_user_waiting: true, has_plan: true, has_todos: false, has_task: false, session_error: "" },
});
assert.ok(item, "toSessionItem must build the item");
assert.equal(item.worktree_missing, false, "worktree_missing passes through");
assert.equal(item.worktree_active, true, "worktree_active passes through (the green tag's only predicate)");
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
assert.match(metaSource, /worktree_active:\s*typeof payload\.session\.worktree_active === "boolean"/, "cache merge must overwrite worktree_active only when the payload carries a boolean");

// ── 4. 视图接线：aux 徽标在名字上方一排，子代理折角回到名字前 ──
// 2026-10-16 用户先要求「小状态图标都放到会话名上面」，随后同一天改主意：
// 折角图标固定出现在子会话名前面（它是子代理的类型标识，不是状态徽标），
// 子会话本身不渲染任何状态徽标。所以这里钉的是**修正后**的布局：
// 上方那排只装 aux/worktree 状态徽标，折角留在 SessionCardIcon。
const card = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionCard.tsx"), "utf8");
const top = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionCardTopBadges.tsx"), "utf8");
const icon = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionCardIcon.tsx"), "utf8");
assert.match(card, /<SessionCardTopBadges/, "SessionCard must render the top badge row");
assert.match(card, /<SessionCardIcon/, "SessionCard must render the icon cell");
assert.equal(card.includes("<SessionStatusBadges"), false, "the aux badges must no longer render under the name");
assert.equal(card.includes("<SessionWorktreeTag"), false, "the worktree tag must no longer render under the name");
assert.match(top, /<SessionWorktreeTag/, "the top row must carry the green worktree tag");
assert.match(top, /<SessionStatusBadges/, "the top row must carry the aux badges");
assert.match(
  top,
  /session\.aux_flags\?\.ask_user_waiting/,
  "the question-mark pulse is driven by aux_flags.ask_user_waiting",
);
assert.match(
  top,
  /renderToolIcon\("ask_user"\)/,
  "the question-mark reuses the task card ask_user icon, not a bespoke SVG",
);
assert.match(
  top,
  /taskAuxBadgeStyle\(true\)/,
  "the question-mark reuses taskAuxBadgeStyle(true) like the task card attention badge",
);
// 子代理折角回到名字前的图标格（用户 2026-10-16 修正）：它是类型标识，不是状态徽标，
// 所以上方那排对子代理整块不渲染。
assert.match(icon, /<SubSessionIcon/, "the subagent marker returns to the icon cell before the name");
assert.equal(top.includes("SubSessionIcon"), false, "the top row must not carry the subagent marker");
// 子代理不渲染任何状态徽标：靠 top 组件开头的早退实现，不是靠条件包单个徽标。
assert.match(top, /if \(isSubagent \|\| /, "子代理整块不渲染：上面那排对子代理直接 return null");
assert.equal(icon.includes("ask_user"), false, "the question-mark must leave the icon cell");
assert.equal(card.includes("session.awaiting_user"), false, "the orphan awaiting_user field must be gone");
const listTypes = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/sessionListTypes.ts"), "utf8");
assert.equal(listTypes.includes("awaiting_user?:"), false, "the orphan awaiting_user field must be gone from the view type");

// ── 5. 复用边界：不另起图标/样式，错误解析不 import app/ ──
const badges = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionStatusBadges.tsx"), "utf8");
assert.match(badges, /from "\.\.\/task\/taskIcons"/, "badge icons/styles reuse the task card set");
assert.match(badges, /renderToolIcon\("todo"\)/, "todo icon reuses the stream tool icon");
assert.match(badges, /renderToolIcon\("task"\)/, "task icon reuses the stream tool icon");

// ── 6. 绿 worktree 标签：只活树显示；定位归上方那排（用户 2026-10-16 定）──
const tag = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionWorktreeTag.tsx"), "utf8");
assert.match(tag, /taskWorktreeTagStyle\("enabled"\)/, "the tag reuses the task card worktree tag style");
assert.equal(tag.includes('position: "absolute"'), false, "the tag no longer positions itself — the top row owns placement now");
// 判据只有后端能给（前端 stat 不了文件系统）：worktree_active = 路径在 + 目录还在
// + 它**确实是** linked worktree（.git 是文件）。原先是前端拿 path 非空 + !worktree_missing
// 自己判 —— 那只问「目录还在不在」，而主 checkout 的目录当然一直在。2026-10-16 实测
// 14 条 related_worktree 里 13 条因此误亮（本项目主 checkout、别的仓库、乃至不是仓库的
// ~/.claude）。行为守卫在 Go 侧：TestRelatedWorktreeActiveOnlyForLinkedWorktree。
assert.match(tag, /return worktreeActive === true/, "the tag is driven by the server-derived worktree_active flag");
assert.equal(tag.includes("relatedWorktree"), false, "the tag must not fall back to reading related_worktree.path itself");
assert.equal(tag.includes("worktreeMissing"), false, "the tag must not fall back to the dir-exists predicate");
assert.match(top, /isWorktreeActive\(session\.worktree_active\)/, "the top-row predicate and the tag share one source");
assert.equal(tag.includes("NoWorktreeIcon"), false, "no-worktree and finished sessions show nothing at all");
assert.equal(tag.includes("worktreeFinishedLabel"), false, "finished sessions show nothing at all");
assert.equal(badges.includes("NoWorktreeIcon"), false, "the badge strip no longer carries the worktree tag");
assert.equal(badges.includes("worktreeFinishedLabel"), false, "the badge strip no longer carries the worktree tag");
// 整排悬在名字上方且不占流：绝对定位 + bottom:100%，行高一个像素都不变。
assert.match(top, /position: "absolute"/, "the top row must not occupy flow — the name and icons keep their places");
assert.match(top, /bottom: "100%"/, "the row hangs above the session name, not below or beside it");
assert.match(top, /left: 0/, "the row is left-aligned with the session name");
// 一排全空就整块不渲染：空容器会在上方白占一段 gap。子代理直接 return null（它不渲染状态徽标）。
assert.match(
  top,
  /if \(isSubagent \|\| !hasSessionTopBadges\(session, isSubagent, unread\)\)/,
  "an empty top row renders nothing",
);
// 徽标条空着也整块不渲染（防它自己被单独复用时留一段空 gap）。
assert.match(badges, /if \(badges\.length === 0\)/, "an empty badge strip renders nothing");
assert.equal(badges.includes('marginTop: "2px"'), false, "the badge strip no longer sits under the name");
assert.equal(badges.includes("from \"../../app/"), false, "view layer must not import app/");
assert.equal(badges.includes("services/"), false, "view layer must not import services/");
// 徽标最坏情况顶端在本行上方 4px，选中底色拓位顶端在上方 7px；2px 行距时
// 前一行仍留有 8px 盒内下内边距 + 2px 外内边距，所以不会压到前一行正文。
// 单项目、外部列表和跨项目分组**内部**的会话行都是 2px；跨项目的项目分组
// **之间**仍为 9px，不能被“回退会话行间距”误改。
const singleList = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionList.tsx"), "utf8");
assert.match(singleList, /flexDirection: "column", gap: "2px"/, "单项目会话行间距保持 2px");
const externalList = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/ExternalSessionList.tsx"), "utf8");
assert.match(externalList, /flexDirection: "column", gap: "2px"/, "外部会话行间距保持 2px");
const multiList = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/MultiProjectSessionList.tsx"), "utf8");
assert.match(multiList, /flexDirection: "column", gap: "9px"/, "跨项目的项目分组间距保持 9px");
assert.match(multiList, /flexDirection: "column", gap: "2px", paddingTop: TOP_BADGE_LIFT_PX/, "跨项目分组内部会话行间距保持 2px");
// 列表顶端留白 ≥ 拓位高度（用户 2026-10-16「间距固定存在」）：第一行选中时底色要向上
// 拓 TOP_BADGE_LIFT_PX 包住徽标，滚动容器按 padding box 裁剪，少 1px 就会被削平。
// 绑同一个常量而不是写死数字：改拓位高度时这几处必须跟着走。
// ExternalSessionList 不在其列 —— 它自己画行、不渲染 SessionCard，没有拓位可言。
assert.match(
  multiList,
  /gap: "2px", paddingTop: TOP_BADGE_LIFT_PX/,
  "项目名与第一个会话之间必须有固定的借位留白，且随拓位高度同步",
);
assert.match(
  singleList,
  /padding: `\$\{TOP_BADGE_LIFT_PX\}px 8px 8px`/,
  "SessionList 顶端留白必须容得下向上拓展的底色",
);

const sharedList = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/sessionListShared.tsx"), "utf8");
assert.match(sharedList, /marginBottom = "0"/, "toggle rows keep their existing spacing unless a caller opts in");
assert.match(sharedList, /marginBottom,\n        border: "none"/, "the shared toggle row must apply the requested lower safety gap");
for (const listFile of ["SessionList", "MultiProjectSessionList"]) {
  const list = fs.readFileSync(
    path.resolve(import.meta.dirname, `../src/components/session/${listFile}.tsx`),
    "utf8",
  );
  assert.match(list, /marginLeft=\{SUB_SESSION_ICON_OFFSET\}\s+marginBottom="2px"/, `${listFile} gives only child-session toggles a 2px lower safety gap`);
}

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
assert.match(top, /taskAuxIconScaleStyle\(\)/, "the question-mark badge wraps its icon in the scale");
assert.equal(icon.includes("taskAuxIconScaleStyle"), false, "the icon cell no longer carries a scaled aux badge");
const taskRows = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/task/TaskCardRows.tsx"), "utf8");
assert.match(taskRows, /taskAuxIconScaleStyle\(\)/, "the task card badge strip wraps its icons in the scale");

// ── 7.5 选中底色向上拓展，包住名字上方那排徽标（用户 2026-10-16）──
// 借位 = 内容盒这一张底自己长高：负外边距把盒顶提上去，等量补进上内边距让名字留在原位，
// 外边距盒总高不变 —— 行高、行距、后面会话的位置全都不动，只是把上一行的留白借来当底色。
assert.match(card, /hasSessionTopBadges\(session, isSubagent, unread\)/, "the lift must use the same predicate as the top row");
assert.match(card, /data-session-top-lift/, "the lift must be identifiable on the box it grows");
assert.match(card, /(selected || hovered) && !editing && hasSessionTopBadges/, "only the selected row gets an upward lift");
// 只能有**一个**绘制面。另起一块绝对定位的底会横向铺满整行（含时间列与 ⋯ 菜单），
// 面积比未借位的选中行大一圈 —— 那正是用户 2026-10-16 报的「借位版选中框面积过大」。
assert.equal(card.split("background: rowBackground").length - 1, 1, "the selected row paints exactly one background surface");
assert.match(card, /marginTop: liftTop \? `-\$\{liftTop\}px` : 0/, "the lift is a negative margin on the content box itself");
assert.match(card, /`\$\{7 \+ liftTop\}px 4px 7px 2px`/, "the same amount returns as top padding so the name stays put");
// 不再有第二张绝对定位的底，也就不需要把整行关进层叠上下文。
assert.equal(card.includes("position: \"absolute\""), false, "the lift must not be a second, separately positioned surface");
assert.equal(card.includes('isolation: "isolate"'), false, "no per-row stacking context — it trapped the row's ⋯ menu under later rows");
assert.match(top, /export const TOP_BADGE_LIFT_PX/, "the lift height lives next to the row it covers");
// 编辑态名字上方那排不渲染，拓展也得跟着停，否则拓出一块盖着空气的深色底。
assert.match(card, /!editing && hasSessionTopBadges/, "no lift while renaming — the top row is not rendered then");

// ── 8. 后端三处 payload + 存储列（源码守卫，防静默回退）──
// 按**包**扫，不钉单个文件名：2026-10-16 后端拆文件（http_sessions.go → http_sessions_list.go、
// appcontext.go → appcontext_broadcast.go、ws.go → ws_broadcast.go）让钉文件名的守卫假红。
// 它挡的是「字段被删」，不是「文件被搬」，所以判据是调用点数量而不是路径。
const apiDir = path.resolve(import.meta.dirname, "../../server/internal/api");
const apiSources = fs
  .readdirSync(apiDir)
  .filter((name) => name.endsWith(".go") && !name.endsWith("_test.go"))
  .map((name) => fs.readFileSync(path.join(apiDir, name), "utf8"))
  .join("\n");
const auxPayloadSites = apiSources.match(/"aux_flags":\s*\w+\.Aux\.AuxFlagsPayload\(\)/g) || [];
assert.ok(auxPayloadSites.length >= 3, `列表 + appcontext 广播 + ws 广播三处 payload 都必须带 aux_flags，实际 ${auxPayloadSites.length} 处`);
// worktree_active 同三处：漏一处就是「刚建好带 worktree 的会话在列表刷新前不亮灯」。
const activePayloadSites = apiSources.match(/"worktree_active":\s*relatedWorktreeActive\(/g) || [];
assert.ok(activePayloadSites.length >= 3, `列表 + appcontext 广播 + ws 广播三处 payload 都必须带 worktree_active，实际 ${activePayloadSites.length} 处`);
assert.match(
  apiSources,
  /func relatedWorktreeActive\([\s\S]{0,600}?gitview\.IsWorktree\(wt\.Path\)/,
  "worktree_active 必须由 linked-worktree 判据（.git 是文件）派生，不能退回「目录还在」",
);
const schema = fs.readFileSync(path.resolve(import.meta.dirname, "../../server/internal/session/manager_sqlite.go"), "utf8");
for (const col of ["aux_ask_user_waiting", "aux_has_plan", "aux_has_todos", "aux_has_task", "aux_session_error"]) {
  assert.ok(schema.includes(col), `sqlite schema must carry ${col}`);
}
});

test("unread badge sits in the top badge row", () => {
  // 用户 2026-10-25「已完成未读任务的会话上方小徽标做好了么」—— 位置就是**名字上方那排**
  // （worktree 标签 / 问号 / plan·todo·task 那一排的最左），不是右侧时间列的圆点。
  // 右侧那一格和「回复中/等你回答」的空心灯挤在一起，两个信号在同一位置迟早读混。
  const top = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionCardTopBadges.tsx"), "utf8");
  const card = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/session/SessionCard.tsx"), "utf8");
  assert.match(top, /unread = false/, "顶排徽标接收 unread");
  assert.match(
    top,
    /export function hasSessionTopBadges\(session: SessionItem, isSubagent: boolean, unread = false\)/,
    "顶排是否渲染的判据必须把 unread 算进去，否则只有未读的会话不显示这一排",
  );
  assert.match(top, /polyline points="20 6 9 17 4 12"/, "未读徽标是一个✓，不是又一个圆点");
  assert.match(top, /background: "var\(--node-badge-bg\)"/, "底色与项目面包屑徽标同一变量（AGENTS.md §11）");
  assert.match(top, /color: nodeColor \|\| "var\(--accent-color\)"/, "图标色跟随项目节点色");
  assert.match(top, /taskAuxBadgeStyle\(false\)/, "复用任务卡徽标盒，尺寸与同排其它徽标一致");
  assert.match(card, /unread=\{unread\}/, "SessionCard 必须把 unread 传给顶排");
  // 右侧时间列不再画未读圆点：同一个信号摆两处，其中一处迟早会被改漏。
  assert.equal(card.includes("0 0 0 2px"), false, "右侧时间列不再画未读圆点");
});

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

import {
  orderTaskGroupedRows,
  rememberTaskSessions,
  resetTaskSessionTally,
  taskGroupAggregateState,
  taskGroupShowsMembers,
  taskSessionGroupHeading,
  taskSessionGroupLabel,
  taskSessionGroupNumber,
  taskSessionMemberLabel,
  taskSessionSeenCount,
} from "../src/components/session/sessionTaskGroups.ts";
import {
  setWorkflowNodeStartsNewSession,
  workflowNodeStartsNewSession,
} from "../src/shared/workflowModel.ts";

const read = (relative) => fs.readFileSync(path.resolve(import.meta.dirname, "../src", relative), "utf8");
const session = (key, extra = {}) => ({ key, session_key: key, ...extra });

// 2026-11-20 用户决策（方案 A）：UI 层按 task_id 分组，零数据改动，组内仍是
// 原来的子会话样式。分组只解决「同一任务的会话散落在列表各处」，不改变归属本身。
test("同任务的顶层会话收成一组并保留原有相对顺序", () => {
  const items = [
    session("a1", { task_id: "task-a", name: "任务 A · 第一条" }),
    session("b1", { task_id: "task-b", name: "任务 B · 唯一一条" }),
    session("a2", { task_id: "task-a", name: "任务 A · 第二条" }),
    session("loose", { name: "手工会话" }),
  ];
  const rows = orderTaskGroupedRows(items);
  assert.deepEqual(
    rows.map((row) => (row.kind === "group" ? `group:${row.taskId}` : `${row.nested ? "-" : ""}${row.session.key}`)),
    ["group:task-a", "-a1", "-a2", "b1", "loose"],
    "分组出现在第一个成员的位置，成员连续排布其余顺序不变",
  );
  assert.equal(rows[0].label, "任务 A");
  assert.equal(rows[0].number, "", "这些会话名里没有任务号，就不显示任务号（不拿 task_id 充数）");
  assert.equal(rows[0].count, undefined, "分组头不再带成员计数（成员本身就是计数）");
  assert.deepEqual(
    rows[0].members.map((member) => member.key),
    ["a1", "a2"],
    "分组行带上成员：指示灯/时间/菜单都要按它们聚合",
  );
  const numbered = orderTaskGroupedRows([
    session("c1", { task_id: "task-c", name: "重构看板 / #91 · run · 一" }),
    session("c2", { task_id: "task-c", name: "重构看板 / #91 · test · 二" }),
  ]);
  assert.equal(numbered[0].number, "91", "任务号从会话名里取");
  assert.deepEqual(
    rows.filter((row) => row.kind === "session" && row.nested).map((row) => row.groupLabel),
    ["任务 A", "任务 A"],
    "成员行带上要剥掉的共享前缀",
  );
  assert.equal(rows[3].nested, false, "单条会话的任务不成组，也不缩进");
  assert.equal(rows[4].nested, false, "非任务会话保持原样");
});

test("只有一个会话的任务与空 task_id 都不分组", () => {
  const rows = orderTaskGroupedRows([
    session("only", { task_id: "task-solo" }),
    session("blank", { task_id: "  " }),
  ]);
  assert.equal(rows.length, 2);
  assert.ok(rows.every((row) => row.kind === "session" && row.nested === false));
});

// 「这一页只有一条成员」不等于「这个任务只有一条会话」：多项目侧栏每根只取 6 条，
// 用户 2026-11-20 报「刷新后分组整个消失」。后端下发的 task_session_count 决定分组。
test("任务还有别的会话没装进这一页时也要成组", () => {
  const rows = orderTaskGroupedRows([
    session("a1", { task_id: "task-a", name: "重构看板 / #91 · run · 一", task_session_count: 4 }),
    session("b1", { task_id: "task-b", name: "另一个任务 / #12 · run · 一", task_session_count: 1 }),
  ]);
  assert.deepEqual(
    rows.map((row) => (row.kind === "group" ? `group:${row.taskId}` : `-${row.session.key}`)),
    ["group:task-a", "-a1", "-b1"],
    "任务 a 已知有 4 条会话，窗口里只有 1 条也成组；任务 b 只有 1 条，平铺",
  );
  assert.equal(rows[0].number, "91");
  assert.equal(rows[2].nested, false);
});

// 用户 2026-11-20：「mindfs 会话很多，当展开的时候是新样式，收起或者切换到其他
// 节点的时候又变回旧样式」。根因是判据只看当前这一页：刷新/翻页把成员挤出窗口，
// 分组当场消失。后端 task_session_count 是跨刷新的权威，进程内记忆兜住它缺席的路径。
test("成员滑出这一页窗口后分组仍然成立（进程内记忆兜底）", () => {
  resetTaskSessionTally();
  const scope = "node-a\u0000root-a";
  rememberTaskSessions(scope, [
    session("a1", { task_id: "task-a", name: "重构看板 / #91 · run · 一" }),
    session("a2", { task_id: "task-a", name: "重构看板 / #91 · test · 二" }),
  ]);
  assert.equal(taskSessionSeenCount(scope, "task-a"), 2);
  assert.equal(taskSessionSeenCount(scope, "task-b"), 0, "没见过的任务不充数");

  // 下一次刷新只回来一条（其余滑出窗口），旧后端也不会下发 task_session_count。
  const rows = orderTaskGroupedRows(
    [session("a1", { task_id: "task-a", name: "重构看板 / #91 · run · 一" })],
    (taskId) => taskSessionSeenCount(scope, taskId),
  );
  assert.deepEqual(
    rows.map((row) => (row.kind === "group" ? `group:${row.taskId}` : `-${row.session.key}`)),
    ["group:task-a", "-a1"],
    "窗口里只剩一条成员，分组头必须还在",
  );

  // 作用域隔离：换节点/换项目后同名 task_id 不能借别人的记忆充数。
  const otherNode = "node-b\u0000root-a";
  assert.equal(taskSessionSeenCount(otherNode, "task-a"), 0);
  assert.equal(
    orderTaskGroupedRows(
      [session("a1", { task_id: "task-a", name: "重构看板 / #91 · run · 一" })],
      (taskId) => taskSessionSeenCount(otherNode, taskId),
    )[0].kind,
    "session",
    "另一个节点上的同名任务照常平铺",
  );
  // 子会话不参与分组，记进来会把阈值虚高。
  rememberTaskSessions(otherNode, [session("c1", { task_id: "task-c", parent_session_key: "a1" })]);
  assert.equal(taskSessionSeenCount(otherNode, "task-c"), 0, "子会话不计入任务会话数");

  // 接线：行序列必须走这份记忆，否则上面那条兜底在生产里不生效。
  const hook = read("components/session/useSessionChildTree.ts");
  assert.match(hook, /rememberTaskSessions\(tallyScope, topLevel\)/);
  assert.match(hook, /orderTaskGroupedRows\(topLevel, \(taskId\) => taskSessionSeenCount\(tallyScope, taskId\)\)/);
  resetTaskSessionTally();
});

test("分组头剥回任务称呼：共享会话名与节点会话名都认", () => {
  assert.equal(taskSessionGroupLabel([session("x", { name: "重构看板 · 把归档做完" })]), "重构看板");
  assert.equal(taskSessionGroupLabel([session("x", { name: "重构看板 / #7 · 把归档做完" })]), "重构看板");
  assert.equal(taskSessionGroupLabel([session("x", {}), session("y", { name: "有名字的那条" })]), "有名字的那条");
  assert.equal(taskSessionGroupLabel([session("x", {})]), "");
});

// 任务号取会话名里的 `/#91`（后端 TaskSessionName 拼的是看板上的数字号），
// 绝不显示 task_id 那串哈希 —— 用户 2026-11-20 看到「# task_270967ce0da8157」。
test("任务号从会话名里取，不拿 task_id 充数", () => {
  assert.equal(taskSessionGroupNumber([session("x", { name: "对话命名问题 / #91 · 实现" })]), "91");
  assert.equal(taskSessionGroupNumber([session("x", { name: "对话命名问题 · 摘要" })]), "", "run 共享会话名里没有任务号");
  assert.equal(taskSessionGroupNumber([session("x", {}), session("y", { name: "任务 B / #12 · 实现" })]), "12");
});

test("分组头文案 = 任务名 + 任务号，和会话名同一行字", () => {
  assert.equal(taskSessionGroupHeading("对话命名问题", "91", "任务会话"), "对话命名问题 # 91");
  assert.equal(taskSessionGroupHeading("", "91", "任务会话"), "任务会话 # 91");
  assert.equal(taskSessionGroupHeading("对话命名问题", "", "任务会话"), "对话命名问题", "没有任务号时只显示任务名");

  const header = read("components/session/TaskSessionGroupHeader.tsx");
  assert.match(header, /taskSessionGroupHeading\(/, "分组头必须走同一套文案函数");
  assert.match(header, /fontSize: "13px"/, "分组头与普通会话行同名号");
  assert.match(header, /fontWeight: 500/, "字重与未选中的会话行一致");
  assert.doesNotMatch(header, /taskGroupCount/, "分组头不再渲染成员计数胶囊");
  assert.doesNotMatch(header, /个会话/, "分组头文案里不出现成员条数");
  assert.match(header, /data-task-group=/, "分组头的定位属性保持不变");
  assert.match(header, /var\(--accent-color\)/, "分组图标跟随主题色");
  assert.match(header, /aria-expanded=\{expanded\}/, "分组头是展开/收起的开关");
  // 用户 2026-11-20：不要下展开箭头，图标自己就是开关，且比会话行的圆大一号。
  assert.doesNotMatch(header, /rotate\(90deg\)/, "不再单独画展开箭头");
  assert.doesNotMatch(header, /M9 5l7 7-7 7/, "箭头图形整条去掉");
  assert.match(header, /width: "20px"/, "图标位比会话行的 18px 大一号");
  assert.match(header, /width="18" height="18"/, "图标图形比会话行的 16px 大一号");
});

test("成员名剥掉分组头的共享前缀，连任务号一起剥", () => {
  assert.equal(taskSessionMemberLabel("对话命名问题 · 把任务改成对话式", "对话命名问题"), "把任务改成对话式");
  assert.equal(taskSessionMemberLabel("对话命名问题 / #91 · 把任务改成对话式", "对话命名问题"), "把任务改成对话式");
  assert.equal(taskSessionMemberLabel("对话命名问题 / #91 · run · 把任务改成对话式", "对话命名问题"), "run · 把任务改成对话式", "节点名是组内区分点，保留");
  assert.equal(taskSessionMemberLabel("对话命名问题", "对话命名问题"), "对话命名问题", "只剩任务名时原样返回，不渲染空名");
  // 用户 2026-11-20 第三次反馈：「『任务详情面板修改 / #87』的子会话还是完整的名字」。
  // 一组会话都没带摘要时，剥完只剩任务号 —— 那是组内唯一的区分点，留着它。
  assert.equal(taskSessionMemberLabel("任务详情面板修改 / #87", "任务详情面板修改"), "#87", "剥完只剩任务号时显示任务号，不回落到完整名字");
  assert.equal(taskSessionMemberLabel("任务详情面板修改  /  #87", "任务详情面板修改"), "#87", "空格写法不敏感");
  assert.equal(taskSessionMemberLabel("改过名的一条", "对话命名问题"), "改过名的一条", "改过名的会话不按前缀硬切");
  assert.equal(taskSessionMemberLabel("对话命名问题续篇 · 另一轮", "对话命名问题"), "对话命名问题续篇 · 另一轮", "前缀必须在词边界上");
  assert.equal(taskSessionMemberLabel("任务 A · 一", ""), "任务 A · 一", "没有分组头时不做任何裁剪");
});

// 2026-11-20 用户决策：默认收起，点分组头才展开；选中的会话在组内时自动展开。
test("任务分组默认收起，点开才展开，选中项所在组自动展开", () => {
  const members = [session("a1", { task_id: "task-a" }), session("a2", { task_id: "task-a" })];
  assert.equal(taskGroupShowsMembers({}, "task-a", members, ""), false, "默认不展开");
  assert.equal(taskGroupShowsMembers({ "task-a": true }, "task-a", members, ""), true, "点开后展开");
  assert.equal(taskGroupShowsMembers({}, "task-a", members, "a2"), true, "选中的会话在组内时必须展开");
  assert.equal(taskGroupShowsMembers({}, "task-a", members, "other"), false, "选中别的会话不影响本组");

  const hook = read("components/session/useSessionChildTree.ts");
  assert.match(hook, /taskGroupShowsMembers\(expandedTaskGroups/, "折叠判据必须走同一套纯函数");
  assert.match(hook, /hiddenTaskIds\.add\(entry\.taskId\)/, "收起时成员行不进入行序列");
  assert.match(hook, /const toggleTaskGroup = \(taskId: string\)/, "分组头必须能切换展开");
});

test("两条侧栏都渲染分组头，并把 nested 交给会话卡", () => {
  const single = read("components/session/SessionList.tsx");
  const multi = read("components/session/MultiProjectSessionList.tsx");
  for (const [name, source] of [["SessionList", single], ["MultiProjectSessionList", multi]]) {
    assert.match(source, /row\.type === "task-group"/, `${name} must branch on task-group rows`);
    assert.match(source, /<TaskSessionGroupHeader/, `${name} must render the group header`);
    assert.match(source, /number=\{row\.number\}/, `${name} must pass the task number`);
    assert.match(source, /expanded=\{row\.expanded\}/, `${name} must pass the collapsed state`);
    assert.match(source, /onToggle=/, `${name} must let the header toggle`);
    assert.match(source, /nested=\{row\.nested\}/, `${name} must indent grouped sessions`);
    assert.match(source, /stripNamePrefix=\{row\.stripNamePrefix\}/, `${name} must strip the shared task prefix`);
  }
  // 前缀只在渲染时剥：名字本身（重命名草稿、落库值）一个字都不能动。
  const card = read("components/session/SessionCard.tsx");
  assert.match(card, /stripNamePrefix \? taskSessionMemberLabel\(displayName, stripNamePrefix\)/, "SessionCard must apply the prefix stripping");
  assert.match(card, /title=\{shownName\}/, "hover title must show the stripped name too");
  assert.match(card, /setDraftName\(name\)/, "rename draft must start from the stored name");
  // 搜索结果是平铺的命中列表，按任务重组会让「第 N 条命中」跳位。
  assert.match(single, /searchResultsMode\s*\?\s*sessions\.map/, "search results must stay flat");
});

test("nested 只借缩进，不改子会话语义", () => {
  const card = read("components/session/SessionCard.tsx");
  const hook = read("components/session/useSessionCard.ts");
  assert.match(card, /paddingLeft: isSubagent \|\| nested \? "10px" : 0/, "grouped rows share the subagent indent");
  assert.match(hook, /const isSubagent = !!session\.parent_session_key/, "isSubagent still derives only from the parent key");
  assert.doesNotMatch(hook, /nested/, "nested must not leak into the subagent flag");
});

// 后端 SessionReuseRun 是默认值；节点级勾选是唯一的显式退出方式。
test("节点「另起会话」写 always_new，取消则回到 Run 共享会话", () => {
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent" }), false, "缺省与其他节点共用会话");
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent", params: { session_reuse_policy: "run" } }), false);
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent", params: { session_reuse_policy: "per_node" } }), true);
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent", params: { session_reuse_policy: "always_new" } }), true);

  const on = setWorkflowNodeStartsNewSession({ prompt: "实现" }, true);
  assert.equal(on.session_reuse_policy, "always_new");
  assert.equal(on.prompt, "实现", "other params must be preserved");
  const off = setWorkflowNodeStartsNewSession(on, false);
  assert.equal("session_reuse_policy" in off, false, "unchecking removes the param so the run session applies");
});

test("节点属性面板接线到同一组判据", () => {
  const inspector = read("components/task/WorkflowNodeInspector.tsx");
  assert.match(inspector, /testId="workflow-node-new-session"/);
  // 开关是图标按钮，按下态来自 workflowNodeStartsNewSession；点击翻转同一个判据。
  assert.match(inspector, /active=\{workflowNodeStartsNewSession\(selected\)\}/);
  assert.match(inspector, /setWorkflowNodeStartsNewSession\(selected\.params, !workflowNodeStartsNewSession\(selected\)\)/);
});

// 用户 2026-11-20：「也给任务会话组加上指示灯和时间(时间按任务组内的最新时间更新)，
// 并且也补上三点操作按钮」。指示灯是聚合态：有在跑就是空心灯，问题与报错按普通
// 会话的问号/感叹号传递，时间取组内最新。
test("分组头聚合状态：在跑亮空心灯，问题/报错按条数传递，时间取组内最新", () => {
  const quiet = taskGroupAggregateState([
    session("a1", { updated_at: "2026-11-20T10:00:00Z" }),
    session("a2", { updated_at: "2026-11-20T12:30:00Z" }),
  ]);
  assert.deepEqual(quiet, {
    running: false,
    awaiting: false,
    askingUser: 0,
    errors: 0,
    latestAt: "2026-11-20T12:30:00Z",
    archived: false,
    pinned: false,
  });

  const busy = taskGroupAggregateState([
    session("a1", { pending: true, updated_at: "2026-11-20T10:00:00Z", aux_flags: { ask_user_waiting: true } }),
    session("a2", { presence: "delegating", updated_at: "2026-11-20T09:00:00Z", aux_flags: { session_error: "boom" } }),
    session("a3", { presence: "awaiting" }),
  ]);
  assert.equal(busy.running, true, "自己回复中与子代理运行中都算「在跑」");
  assert.equal(busy.awaiting, true);
  assert.equal(busy.askingUser, 1, "问号按条数传递");
  assert.equal(busy.errors, 1, "报错按条数传递");
  assert.equal(busy.latestAt, "2026-11-20T10:00:00Z", "时间取组内最新，不被更老的成员拉回去");

  assert.equal(taskGroupAggregateState([]).archived, false, "空组不算已归档");
  assert.equal(
    taskGroupAggregateState([session("x", { archived_at: "2026-11-20T00:00:00Z" })]).archived,
    true,
    "成员全归档才算归档组（归档面板里菜单要变成「取消归档」）",
  );
  assert.equal(
    taskGroupAggregateState([session("x", { archived_at: "2026-11-20T00:00:00Z" }), session("y")]).archived,
    false,
    "还有没归档的成员就不算归档组",
  );

  const header = read("components/session/TaskSessionGroupHeader.tsx");
  assert.match(header, /taskGroupAggregateState\(members\)/, "分组头必须按成员聚合，不能拿单条会话充数");
  assert.match(header, /mindfs-hollow-pulse/, "有在跑就是空心灯，和会话行同一个动画");
  assert.match(header, /formatTime\(aggregate\.latestAt/, "时间按组内最新时间显示");
  assert.match(header, /renderToolIcon\("ask_user"\)/, "问号沿用普通会话的徽标图标");
  assert.match(header, /<TaskSessionErrorIcon \/>/, "感叹号沿用普通会话的徽标图标");
  assert.match(header, /<TaskSessionGroupMenu/, "分组头挂三点菜单");
});

// 用户 2026-11-20：「会话组的时间没有对齐」「灯也没对齐」。根因是右侧信息列被放进了
// 展开按钮内部，而按钮有 4px 右内边距；会话卡里这一列是**行的直接子元素**。
test("分组头的指示灯/时间与会话行逐像素对齐（信息列在按钮之外）", () => {
  const header = read("components/session/TaskSessionGroupHeader.tsx");
  const card = read("components/session/SessionCard.tsx");
  const buttonBlock = header.slice(header.indexOf("<button"), header.indexOf("</button>"));
  assert.doesNotMatch(buttonBlock, /formatTime|mindfs-hollow-pulse/, "时间与指示灯不能落在可点的盒子里（会整体左移 4px）");
  const afterButton = header.slice(header.indexOf("</button>"));
  assert.match(afterButton, /formatTime\(aggregate\.latestAt/, "信息列是行的直接子元素，排在按钮之后");
  // 右侧列的几何与会话行同一份：minWidth 24px + 右对齐 + 10px 次级字。
  for (const [name, source] of [["SessionCard", card], ["TaskSessionGroupHeader", header]]) {
    assert.match(source, /minWidth: "24px"/, `${name} must keep the 24px info column`);
    assert.match(source, /justifyContent: "flex-end"/, `${name} must right-align the info column`);
    assert.match(source, /paddingLeft: "2px"/, `${name} must keep the same left padding`);
    assert.match(source, /fontSize: "10px"/, `${name} must use the same time font size`);
  }
});

// 用户 2026-11-20：「置顶按钮不应该移除」。分组没有自己的置顶条目，置顶是按会话
// 存的，所以整组口径 = 成员全部置顶；动作把全部成员写上/清掉，不弹确认。
test("分组菜单保留置顶，整组生效", () => {
  const pinnedAll = taskGroupAggregateState([
    session("a1", { pinned_at: "2026-11-20T10:00:00Z" }),
    session("a2", { pinned_at: "2026-11-20T11:00:00Z" }),
  ]);
  assert.equal(pinnedAll.pinned, true, "成员全部置顶才算整组置顶");
  assert.equal(
    taskGroupAggregateState([session("a1", { pinned_at: "2026-11-20T10:00:00Z" }), session("a2")]).pinned,
    false,
    "半置顶按未置顶显示，再点一下把剩下的补齐",
  );
  assert.equal(taskGroupAggregateState([]).pinned, false, "空组不算置顶");

  const menu = read("components/session/TaskSessionGroupMenu.tsx");
  assert.match(menu, /<PinIcon pinned=\{pinned\} \/>/, "置顶项沿用会话卡同一颗图钉");
  assert.match(menu, /void pinTaskGroup\(\{ members, pinned: !pinned, onPin \}\)/, "置顶走整组动作");
  assert.match(menu, /\{pinned \? t\("sessionList\.unpin"\) : t\("sessionList\.pin"\)\}/, "菜单文案随整组状态切换");
  assert.ok(
    menu.indexOf("pinTaskGroup") < menu.indexOf("taskGroupSyncUnsupported"),
    "置顶排在置灰项之前，与会话卡菜单同序",
  );

  const actions = read("components/session/taskGroupActions.ts");
  assert.match(actions, /export async function pinTaskGroup/, "整组置顶是一个显式动作");
  assert.match(actions, /if \(!onPin \|\| targets\.length === 0\) return;/, "没有置顶出口或没有成员时不发请求");
  const pinStart = actions.indexOf("export async function pinTaskGroup");
  const pinBlock = actions.slice(pinStart, actions.indexOf("export async function", pinStart + 1));
  assert.doesNotMatch(pinBlock, /confirmDialog/, "置顶可逆，不弹确认（与单条置顶一致）");
  assert.match(pinBlock, /for \(const session of targets\)[\s\S]{0,80}?await onPin\(session, pinned\)/, "逐条串行写，避免整表覆盖互相打架");

  const header = read("components/session/TaskSessionGroupHeader.tsx");
  assert.match(header, /pinned=\{aggregate\.pinned\}/, "分组头把整组置顶状态交给菜单");
  const single = read("components/session/SessionList.tsx");
  const multi = read("components/session/MultiProjectSessionList.tsx");
  for (const [name, source] of [["SessionList", single], ["MultiProjectSessionList", multi]]) {
    assert.match(source, /onPin=\{onPin\}/, `${name} must wire group pin`);
  }
});

// 用户 2026-11-20：「禁用掉重命名和同步功能，归档和删除会递归操作全部，有额外的提示」。
test("分组三点菜单：重命名/同步置灰，归档与删除递归全部成员且先确认一次", () => {
  const menu = read("components/session/TaskSessionGroupMenu.tsx");
  assert.match(
    menu,
    /disabled[\s\S]{0,120}?sessionList\.taskGroupRenameUnsupported/,
    "重命名保留但置灰，并说明原因",
  );
  assert.match(
    menu,
    /disabled[\s\S]{0,120}?sessionList\.taskGroupSyncUnsupported/,
    "同步保留但置灰，并说明原因",
  );
  assert.match(menu, /void archiveTaskGroup\(\{ members, name, archived, t, onArchive \}\)/, "归档走整组动作");
  assert.match(menu, /void deleteTaskGroup\(\{ members, name, t, onDelete \}\)/, "删除走整组动作");
  assert.match(menu, /\{onDelete \? \(/, "归档视图没有删除入口时不渲染删除项");
  assert.match(menu, /setOpen\(false\)/, "点了任一项都先收起菜单");

  const actions = read("components/session/taskGroupActions.ts");
  assert.match(actions, /count: targets\.length/, "确认框写明影响条数（组内顶层成员数）");
  assert.match(actions, /sessionList\.confirmArchiveTaskGroup/);
  assert.match(actions, /sessionList\.confirmUnarchiveTaskGroup/);
  assert.match(actions, /sessionList\.confirmDeleteTaskGroup/);
  assert.match(actions, /danger: true/, "删除确认是危险样式");
  assert.match(actions, /onDelete\(session, \{ confirm: false \}\)/, "整组只确认一次，逐条不再弹框");
  assert.match(
    actions,
    /for \(const session of targets\) \{[\s\S]{0,120}?await /,
    "逐条串行执行，避免 N 份列表状态并发互相覆盖",
  );

  // confirm:false 只在整组删除这一条路径上出现；单条删除仍必须自己确认。
  const archive = read("app/useSessionArchive.ts");
  assert.match(archive, /options\?\.confirm !== false/, "唯一删除出口要认整组确认过的标记");
  assert.match(archive, /sessionList\.confirmDeleteSession/, "单条删除的确认不能丢");
});

test("分组行把成员带到两条侧栏，菜单动作接到会话归档/删除出口", () => {
  const hook = read("components/session/useSessionChildTree.ts");
  assert.match(
    hook,
    /rows\.push\(\{ type: "task-group"[\s\S]{0,160}?members \}\)/,
    "分组成员必须随行下发，否则分组头无从聚合",
  );
  const single = read("components/session/SessionList.tsx");
  const multi = read("components/session/MultiProjectSessionList.tsx");
  for (const [name, source] of [["SessionList", single], ["MultiProjectSessionList", multi]]) {
    assert.match(source, /members=\{row\.members/, `${name} must hand members to the group header`);
    assert.match(source, /onArchive=\{onArchive\}/, `${name} must wire group archive`);
    assert.match(source, /onDelete=\{onDelete\}/, `${name} must wire group delete`);
  }
  // 多项目分组的会话条目不一定带 root_id（项目号在 group 上），整组归档/删除
  // 必须按 group.rootId 定位项目库，否则会落到「当前选中项目」。
  assert.match(
    multi,
    /members=\{row\.members\.map\(\(member\) => \(\{ \.\.\.member, root_id: member\.root_id \|\| group\.rootId \}\)\)\}/,
    "multi-project group members must carry their project id",
  );
});

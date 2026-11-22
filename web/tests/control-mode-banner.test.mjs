import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import {
  controlModeBanner,
  controlModeLabel,
  conflictReminder,
  controlSyncActionFor,
  isControlMode,
  newestModeEventTo,
} from "../src/shared/controlMode.ts";
import { toControlSyncStatus } from "../src/services/net/controlSync.ts";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const service = read("../src/services/net/controlSync.ts");
const shared = read("../src/shared/controlMode.ts");
const hook = read("../src/hooks/useControlSyncStatus.ts");
const banner = read("../src/components/app/ControlModeBanner.tsx");
const rootView = read("../src/app/AppRootView.tsx");
const zh = read("../src/i18n/locales/zh-CN-workspace.ts");
const en = read("../src/i18n/locales/en-US-workspace.ts");

test("未启用时不出横幅；在线出弱化横幅并带切本地入口", () => {
  // 端点 404（主节点 / 没开 control_sync）就是「没有这个能力」：一律不渲染，
  // 而不是在每个普通节点上挂一条装饰条。
  assert.equal(controlModeBanner(null), null);
  assert.equal(controlModeBanner(undefined), null);
  assert.equal(isControlMode("online"), true);
  assert.equal(isControlMode("bogus"), false);
  assert.equal(isControlMode(undefined), false);

  // 在线也可见（T26）：主节点随时会断，用户得有看得见的入口主动切到本地。
  const online = controlModeBanner("online");
  assert.equal(online?.tone, "online");
  assert.deepEqual(online?.actions, ["switch-local"]);
  assert.ok(online?.title && online?.detail);
});

test("三种降级状态各自给出动作，攒了冲突才多出查看入口", () => {
  // 动作是纯函数输出，按钮渲染必须照它走：把「本地模式」画成「查看冲突」这种
  // 错位在页面上看不出来，只会点出一个不存在的面板。
  const local = controlModeBanner("offline-local");
  assert.equal(local?.tone, "local");
  assert.deepEqual(local?.actions, ["retry"]);

  // 离线期间攒下冲突：重试之外还要能把人带到面板前。
  const localWithConflicts = controlModeBanner("offline-local", { conflictCount: 2 });
  assert.deepEqual(localWithConflicts?.actions, ["retry", "view-conflicts"]);

  const syncing = controlModeBanner("reconciling");
  assert.equal(syncing?.tone, "syncing");
  assert.deepEqual(syncing?.actions, ["switch-local"]);

  const conflict = controlModeBanner("conflict-review");
  assert.equal(conflict?.tone, "conflict");
  assert.deepEqual(conflict?.actions, ["view-conflicts"]);

  // 四种动作合起来必须覆盖「切本地 / 重试 / 查看冲突 / 接受新主节点」。
  const all = new Set(
    [
      controlModeBanner("online"),
      local,
      localWithConflicts,
      controlModeBanner("offline-local", { blockedCode: "origin-mismatch" }),
      syncing,
      conflict,
    ].flatMap((s) => s.actions),
  );
  assert.deepEqual([...all].sort(), ["adopt-primary", "retry", "switch-local", "view-conflicts"]);

  // 每个降级状态都得有标题与说明，缺一条横幅就只剩一个色块。
  for (const state of [local, syncing, conflict]) {
    assert.ok(state?.title);
    assert.ok(state?.detail);
  }
});

test("冲突提醒按事件 id 只提醒一次", () => {
  const at = "2026-11-22T10:00:00Z";
  const conflictStatus = (id, count = 2) => ({
    mode: "conflict-review",
    conflict_count: count,
    recent_events: [
      { id, from: "reconciling", to: "conflict-review", at },
      { id: "mode:online->reconciling:1", from: "online", to: "reconciling", at },
    ],
  });

  const first = conflictReminder(conflictStatus("mode:reconciling->conflict-review:7"), new Set());
  assert.equal(first?.id, "mode:reconciling->conflict-review:7");

  // 关掉之后同一次进入不再弹回来 —— 这是「一次性」的全部意义。
  const dismissed = new Set([first.id]);
  assert.equal(conflictReminder(conflictStatus("mode:reconciling->conflict-review:7"), dismissed), null);

  // 走完一轮又撞上新冲突：新事件 id，要再提醒一次。
  const again = conflictReminder(conflictStatus("mode:reconciling->conflict-review:9"), dismissed);
  assert.equal(again?.id, "mode:reconciling->conflict-review:9");

  // 不在冲突模式、或冲突已经裁决干净，都不提醒。
  assert.equal(conflictReminder({ mode: "online", recent_events: [] }, new Set()), null);
  assert.equal(conflictReminder(conflictStatus("id-x", 0), new Set()), null);
  assert.equal(conflictReminder(null, new Set()), null);
});

test("模式标签与事件历史同源", () => {
  assert.equal(controlModeLabel("online"), "controlMode.online");
  assert.equal(controlModeLabel("offline-local"), "controlMode.local");
  assert.equal(controlModeLabel("reconciling"), "controlMode.syncing");
  assert.equal(controlModeLabel("conflict-review"), "controlMode.conflict");
  assert.equal(newestModeEventTo([], "conflict-review"), null);
});

test("状态端点走页面服务器，404 视为未启用而不是故障", () => {
  // 控制面属于页面服务器（主节点），不能跟随选中节点；主节点/未启用时端点 404，
  // 必须静默隐藏横幅 —— 把它当请求失败会在每台普通节点上弹一个错误。
  assert.match(service, /controlPath\("\/api\/control-sync\/status"\)/);
  assert.match(service, /status === 404/);
  // 折叠必须发生在读状态这一个函数里：只在另一处（模式端点）兜住 404，
  // 轮询仍会把 404 抛给组件，横幅每 10 秒冒一次红。按函数边界切开再看，
  // 否则正则会一路吃到 switchControlSyncMode 的同款分支上，等于没钉。
  const fetchStatusBody = service
    .split("export async function fetchControlSyncStatus")[1]
    .split("export async function")[0];
  assert.match(fetchStatusBody, /if \(endpointMissing\(err\)\) return null;/);
});

test("模式端点只发 local / retry / adopt_primary 三种动作", () => {
  assert.match(service, /controlPath\("\/api\/control-sync\/mode"\)/);
  assert.match(service, /method: "POST"/);
  assert.match(service, /JSON\.stringify\(\{ action \}\)/);
  const actions = [...shared.matchAll(/type ControlSyncAction = ([^;]+);/g)];
  assert.equal(actions.length, 1, "动作类型必须只有一处定义");
  assert.match(actions[0][1], /"local"/);
  assert.match(actions[0][1], /"retry"/);
  // 服务端 handleControlSyncMode 认的就是这三个；写错一个字会 400，界面上是「点了没反应」。
  assert.match(actions[0][1], /"adopt_primary"/);
  assert.doesNotMatch(actions[0][1], /"switch"|"online"/);
});

test("横幅轮询状态，未启用后不再重试", () => {
  assert.match(hook, /setInterval\(/);
  assert.match(hook, /clearInterval\(/);
  // 轮询拿回 null（端点 404）后要停表：继续每 10 秒打一次 404 没有意义，
  // 也会在浏览器控制台刷满失败请求。
  assert.match(hook, /if \(!next\)[\s\S]{0,120}setEnabled\(false\)/);
  assert.match(hook, /if \(!enabled\) return/);
  // 视图层只消费 hook：直接 import services 会踩「view -> service」架构红线。
  assert.match(banner, /useControlSyncStatus\(\)/);
  assert.doesNotMatch(banner, /\bservices\//);
});

test("横幅四个动作各自打到对应的请求", () => {
  // 查看冲突只派发事件（面板自己订阅），不发控制面请求；
  // 切本地与重试才走 mode 端点，且 action 取值必须与服务端一致。
  const runAction = hook.split("const runAction")[1].split("const [")[0];
  assert.match(runAction, /action === "view-conflicts"[\s\S]{0,200}CONTROL_CONFLICTS_EVENT/);
  // 横幅动作与服务端动作是两套词，对照只在 controlSyncActionFor 一处。
  assert.match(runAction, /switchControlSyncMode\(controlSyncActionFor\(action\)\)/);
  assert.match(hook, /switchControlSyncMode/);
  // blocked_code 必须从状态透传到纯映射：漏了这一行，采纳入口永远不会出现。
  assert.match(hook, /blockedCode: status\?\.blocked_code/);
  // 提醒的「关闭」按事件 id 记，不是按模式记 —— 否则下一拍轮询又会弹回来。
  assert.match(hook, /new Set\(prev\)\.add\(reminder\.id\)/);
});

test("App 根视图挂上横幅，且只挂一处", () => {
  assert.match(rootView, /import \{ ControlModeBanner \} from "\.\.\/components\/app\/ControlModeBanner"/);
  assert.equal((rootView.match(/<ControlModeBanner \/>/g) || []).length, 1);
});

test("中英文都补齐横幅文案", () => {
  for (const key of [
    "controlMode.online",
    "controlMode.onlineDetail",
    "controlMode.local",
    "controlMode.localDetail",
    "controlMode.syncing",
    "controlMode.syncingDetail",
    "controlMode.conflict",
    "controlMode.conflictDetail",
    "controlMode.retry",
    "controlMode.retrying",
    "controlMode.switchLocal",
    "controlMode.viewConflicts",
    "controlMode.conflictReminder",
    "controlMode.dismiss",
    "controlMode.adoptPrimary",
    "controlMode.adoptWarning",
    "controlMode.adoptConfirm",
    "controlMode.cancel",
  ]) {
    assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `中文缺 ${key}`);
    assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `英文缺 ${key}`);
  }
  // 提醒文案要带条数占位符，否则「检测到 N 条」只能说成「有冲突」。
  assert.match(zh, /controlMode\.conflictReminder": "[^"]*\{count\}/);
  assert.match(en, /controlMode\.conflictReminder": "[^"]*\{count\}/);
});
test("只有身份不符的锁才给「接受新主节点」", () => {
  // 主节点被重装/换机时副本按旧主节点建的，永远追不平 —— 采纳是唯一出路。
  const adopted = controlModeBanner("offline-local", { blockedCode: "origin-mismatch" });
  assert.ok(adopted.actions.includes("adopt-primary"));

  // schema 不兼容采纳也没用（换台机器还是同一个版本），不能给这个会丢副本的入口。
  const schema = controlModeBanner("offline-local", { blockedCode: "schema-mismatch" });
  assert.deepEqual(schema.actions, ["retry"]);

  // 没锁、或服务端给了个认不出来的原因：照旧只给重试。
  assert.deepEqual(controlModeBanner("offline-local").actions, ["retry"]);
  assert.deepEqual(controlModeBanner("offline-local", { blockedCode: "something-new" }).actions, ["retry"]);

  // 采纳只属于本地模式：别处出现说明动作表被串了。
  for (const mode of ["online", "reconciling", "conflict-review"]) {
    assert.ok(!controlModeBanner(mode, { blockedCode: "origin-mismatch" }).actions.includes("adopt-primary"));
  }
});

test("横幅动作到服务端动作只有一处对照", () => {
  assert.equal(controlSyncActionFor("switch-local"), "local");
  assert.equal(controlSyncActionFor("retry"), "retry");
  assert.equal(controlSyncActionFor("adopt-primary"), "adopt_primary");
  // 查看冲突不发控制面请求，落到 else 也只是 retry —— 调用点在发请求前就分流了。
  assert.equal(controlSyncActionFor("view-conflicts"), "retry");
});

test("接受新主节点是两步确认，且写明会丢什么", () => {
  // 这是整套界面里唯一会丢弃副本的动作：第一下只展开确认条，第二下才发请求。
  assert.match(banner, /const \[confirmAdopt, setConfirmAdopt\] = useState\(false\)/);
  assert.match(banner, /action === "adopt-primary" && confirmAdopt/);
  assert.match(banner, /if \(action === "adopt-primary"\) \{[\s\S]{0,80}setConfirmAdopt\(true\)/);
  // 确认条里必须先有警告文案与「取消」，不能只剩一个确认键。
  const confirmBlock = banner.split('action === "adopt-primary" && confirmAdopt')[1].split("return (")[1];
  assert.match(confirmBlock, /controlMode\.adoptWarning/);
  assert.match(confirmBlock, /controlMode\.cancel/);
  // 模式一变就收回确认态，免得「确认接受」在下一轮横幅里还挂着。
  assert.match(banner, /useEffect\(\(\) => \{[\s\S]{0,60}setConfirmAdopt\(false\)/);
});

test("状态端点带回的锁码与事件不能在服务层被丢掉", () => {
  // 真机教训：后端 /status 已经给了 blocked_code 与 recent_events，但服务层
  // 只挑了四个字段，页面上「接受新主节点」和事件历史永远出不来。这里直接钉
  // 解析后的完整对象，不再只靠源码字符串断言。
  const at = "2026-11-22T11:21:58Z";
  const parsed = toControlSyncStatus({
    mode: "offline-local",
    base_rev: 7,
    primary_reachable: true,
    pending_count: 2,
    conflict_count: 3,
    blocked_reason: "controlsync: primary instance changed",
    blocked_code: "origin-mismatch",
    last_primary_at: at,
    last_error: "boom",
    recent_events: [
      { id: "e1", from: "reconciling", to: "offline-local", reason: "changed", at },
      { id: "bad", from: "online", to: "bogus", at },
    ],
  });
  assert.deepEqual(parsed, {
    mode: "offline-local",
    base_rev: 7,
    primary_reachable: true,
    pending_count: 2,
    conflict_count: 3,
    blocked_reason: "controlsync: primary instance changed",
    blocked_code: "origin-mismatch",
    last_primary_at: at,
    last_error: "boom",
    recent_events: [
      { id: "e1", from: "reconciling", to: "offline-local", reason: "changed", at },
    ],
  });
  // 少了 blocked_code，纯映射再正确也不会给采纳入口；把解析结果接上映射验证。
  assert.ok(
    controlModeBanner(parsed?.mode, {
      blockedCode: parsed?.blocked_code,
      conflictCount: parsed?.conflict_count,
    }).actions.includes("adopt-primary"),
  );
  assert.equal(toControlSyncStatus({ mode: "bogus" }), null);
});

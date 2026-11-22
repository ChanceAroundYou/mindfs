import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { controlModeBanner, isControlMode } from "../src/shared/controlMode.ts";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

const service = read("../src/services/net/controlSync.ts");
const shared = read("../src/shared/controlMode.ts");
const hook = read("../src/hooks/useControlSyncStatus.ts");
const banner = read("../src/components/app/ControlModeBanner.tsx");
const rootView = read("../src/app/AppRootView.tsx");
const zh = read("../src/i18n/locales/zh-CN-workspace.ts");
const en = read("../src/i18n/locales/en-US-workspace.ts");

test("在线与未启用时不出横幅", () => {
  // 主节点和没开 control_sync 的机器也会被问到 —— 一律「没有横幅」，
  // 而不是渲染一个永远显示「在线」的装饰条。
  assert.equal(controlModeBanner(null), null);
  assert.equal(controlModeBanner("online"), null);
  assert.equal(isControlMode("online"), true);
  assert.equal(isControlMode("bogus"), false);
  assert.equal(isControlMode(undefined), false);
});

test("三种降级状态各自给出唯一动作", () => {
  // 动作是纯函数输出，按钮渲染必须照它走：把「本地模式」画成「查看冲突」这种
  // 错位在页面上看不出来，只会点出一个不存在的面板。
  const local = controlModeBanner("offline-local");
  assert.equal(local?.tone, "local");
  assert.equal(local?.action, "retry");

  const syncing = controlModeBanner("reconciling");
  assert.equal(syncing?.tone, "syncing");
  assert.equal(syncing?.action, "switch-local");

  const conflict = controlModeBanner("conflict-review");
  assert.equal(conflict?.tone, "conflict");
  assert.equal(conflict?.action, "view-conflicts");

  // 三个状态都得有标题与说明，缺一条横幅就只剩一个色块。
  for (const state of [local, syncing, conflict]) {
    assert.ok(state?.title);
    assert.ok(state?.detail);
  }
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

test("模式端点只发 local / retry 两种动作", () => {
  assert.match(service, /controlPath\("\/api\/control-sync\/mode"\)/);
  assert.match(service, /method: "POST"/);
  assert.match(service, /JSON\.stringify\(\{ action \}\)/);
  const actions = [...shared.matchAll(/type ControlSyncAction = ([^;]+);/g)];
  assert.equal(actions.length, 1, "动作类型必须只有一处定义");
  assert.match(actions[0][1], /"local"/);
  assert.match(actions[0][1], /"retry"/);
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

test("App 根视图挂上横幅，且只挂一处", () => {
  assert.match(rootView, /import \{ ControlModeBanner \} from "\.\.\/components\/app\/ControlModeBanner"/);
  assert.equal((rootView.match(/<ControlModeBanner \/>/g) || []).length, 1);
});

test("中英文都补齐横幅文案", () => {
  for (const key of [
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
  ]) {
    assert.match(zh, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `中文缺 ${key}`);
    assert.match(en, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `英文缺 ${key}`);
  }
});
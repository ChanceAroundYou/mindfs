// 置顶的契约（2026-10-05 起）：权威在**服务端主节点**，不是 localStorage。
//
// 这些测试守的每一条都对应一个具体的坏法：
//   1. 前端自己拼会话置顶键 → 与服务端分叉，症状「置顶了刷新就没」
//   2. 用 appPath/appURL 而非 controlPath → 置顶跟着选中节点走，PC 上置顶存进 PC
//   3. 持久化缓存层（localStorage）→ 制造「双份真相」，迟早分叉
//   4. 快照只抹不盖 → worker 的列表永远没有置顶区
//   5. 刷新依赖写空数组 → 切项目后看到的还是首次那份
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const SRC = path.resolve(import.meta.dirname, "../src");

function loadModule(relPath) {
  const file = path.resolve(SRC, relPath);
  const compiled = ts.transpileModule(fs.readFileSync(file, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  // 与 session-list-merge.test.mjs 同一形状：transpile 成 CommonJS 后在 vm 里跑。
  // 只桩掉两个 import：controlPlane（路径拼接）与 api（发请求，测试里不真发）。
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (name) => {
      if (name.includes("controlPlane")) return { controlPath: (p) => `/page${p}` };
      if (name.includes("api")) return { protectedJSON: async () => ({}) };
      throw new Error(`unexpected require: ${name}`);
    },
    console,
    URLSearchParams,
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return sandbox.exports;
}

const pins = loadModule("services/pins.ts");

// ── 键的形状：必须与服务端 sessionPinKey 逐字节一致 ──
assert.equal(pins.sessionPinKey("CMAI", "s1"), "CMAI::s1");
assert.equal(pins.sessionPinKey("", "s1"), "s1");
assert.equal(pins.sessionPinKey(" CMAI ", " s1 "), "CMAI::s1");
assert.equal(pins.sessionPinKey("CMAI", "  "), "", "空会话键必须拒绝，否则会写进一条垃圾记录");

// Go 侧 pins.SessionScopeKey 与之同形（服务端另有 sessionPinKey，形状相同）
const goStore = fs.readFileSync(path.resolve(SRC, "../../server/internal/pins/store.go"), "utf8");
assert.ok(
  goStore.includes('return n + ScopeSep + r + ScopeSep + k'),
  "服务端 ScopeKey 仍是三段式；会话置顶键改为两段（rootID::key）后这里必须同步改",
);

// ── 归一化：坏值必须被剔掉，而不是变成 NaN 键 ──
const normalized = pins.normalizePins({
  projects: { "pc::CMAI": 1000, bad: "x", zero: 0, empty: "" },
  sessions: { "CMAI::s1": "2026-10-05T00:00:00Z", bad: 123, empty: "  " },
});
// 注意用 JSON 比而不是 deepEqual：vm 里造出来的对象原型来自另一个 realm，
// deepEqual 的原型检查会因此判它们「结构相同但不相等」。
assert.equal(JSON.stringify(Object.keys(normalized.projects)), JSON.stringify(["pc::CMAI"]));
assert.equal(JSON.stringify(Object.keys(normalized.sessions)), JSON.stringify(["CMAI::s1"]));

// 顶层容错：回包整个不是对象时给空表，不能抛
for (const junk of [null, "nonsense", 42, undefined]) {
  const out = pins.normalizePins(junk);
  assert.equal(JSON.stringify(out), JSON.stringify({ projects: {}, sessions: {} }), `垃圾回包 ${junk} 应归一成空表`);
}

// ── 按项目取会话置顶：给 worker 列表叠加用 ──
const snapshot = {
  "CMAI::s1": "2026-10-05T00:00:00Z",
  "CMAI::s2": "2026-10-04T00:00:00Z",
  "docs::d1": "2026-10-03T00:00:00Z",
};
const forCmai = pins.sessionPinsForRoot(snapshot, "CMAI");
assert.equal(JSON.stringify([...forCmai.keys()].sort()), JSON.stringify(["s1", "s2"]), "别的项目的键不该进来");
assert.equal(forCmai.get("s1"), "2026-10-05T00:00:00Z");
assert.equal(pins.sessionPinsForRoot(snapshot, "docs").size, 1);
assert.equal(pins.sessionPinsForRoot(snapshot, "没有这个项目").size, 0);

// ── 请求必须打页面服务器（控制面），不能跟随选中节点 ──
const pinsSource = fs.readFileSync(path.resolve(SRC, "services/pins.ts"), "utf8");
assert.ok(
  !/\bappPath\(|\bappURL\(/.test(pinsSource),
  "置顶是控制面：请求必须走 controlPath。用 appPath/appURL 会让置顶存进当前选中的节点",
);
assert.ok(pinsSource.includes('from "./controlPlane"'), "必须从 controlPlane 引入路径");
// 只查**代码**：注释里提到 localStorage 是正常的（那是在解释它为什么被删）。
const pinsCode = pinsSource.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, "");
assert.ok(!/localStorage/.test(pinsCode), "不再有 localStorage 缓存层");

// 旧的服务端路径不得再被引用（它已经不存在了）
const prefsSource = fs.readFileSync(path.resolve(SRC, "services/preferences.ts"), "utf8");
assert.ok(
  !prefsSource.includes("session-project-pins"),
  "项目置顶已搬出 preferences；那里还留着就是会打到 404 的死代码",
);
assert.ok(!prefsSource.includes("SessionProjectPins"), "preferences 里不该再有置顶的类型与函数");

// ── 快照必须双向：盖上 + 抹掉 ──
const mergeSource = fs.readFileSync(path.resolve(SRC, "services/sessionListMerge.ts"), "utf8");
assert.ok(
  /pinnedAtByKey\?\.get\(key\)/.test(mergeSource),
  "快照必须能**盖上**置顶：worker 的列表自带 pinnedKeys 恒为空，不盖就等于没置顶",
);
const mergeModule = loadModule("services/sessionListMerge.ts");
const stamped = mergeModule.applyPinnedSnapshotToSessions(
  [
    { key: "s1", root_id: "pc-proj", updated_at: "2026-10-01T00:00:00Z" },
    { key: "s2", root_id: "pc-proj", updated_at: "2026-10-02T00:00:00Z" },
  ],
  "pc-proj",
  ["s1"],
  new Map([["s1", "2026-10-05T00:00:00Z"]]),
);
assert.equal(stamped[0].key, "s1", "主节点置顶的会话必须排到最前");
assert.equal(stamped[0].pinned_at, "2026-10-05T00:00:00Z", "置顶时间取主节点那份");
assert.equal(stamped.find((i) => i.key === "s2").pinned_at, undefined);

// 快照里没有的旧置顶必须被抹掉（会话库退役列的残留值）
const wiped = mergeModule.applyPinnedSnapshotToSessions(
  [{ key: "s1", root_id: "r", pinned_at: "2026-01-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" }],
  "r",
  [],
);
assert.equal(wiped[0].pinned_at, undefined, "快照外的残留置顶必须抹掉，否则点了没反应");

// 别的项目不受影响
const otherRoot = mergeModule.applyPinnedSnapshotToSessions(
  [{ key: "s1", root_id: "other", pinned_at: "2026-01-01T00:00:00Z" }],
  "r",
  ["s1"],
);
assert.equal(otherRoot[0].pinned_at, "2026-01-01T00:00:00Z", "快照只作用于它自己的项目");

// ── 刷新时机：切项目要重拉 ──
const appSource = fs.readFileSync(path.resolve(SRC, "App.tsx"), "utf8");
assert.ok(
  appSource.includes("void refreshPinsFromServer()"),
  "切项目时要顺带刷新一次置顶（用户定的同步时机）",
);
// 定位那个「切项目时刷新」的 effect 本身（不是它的定义处 —— 两者隔了一万行）。
const pinEffectAt = appSource.indexOf("void refreshPinsFromServer()");
assert.ok(pinEffectAt > 0, "切项目时要顺带刷新一次置顶");
const pinEffect = appSource.slice(pinEffectAt, pinEffectAt + 300);
assert.ok(
  /\}, \[currentRootId\]\)/.test(pinEffect),
  "刷新 effect 必须依赖 currentRootId —— 写空数组就是「切了项目还看到首次那份」的老 bug",
);

const listSource = fs.readFileSync(path.resolve(SRC, "components/SessionList.tsx"), "utf8");
assert.ok(
  !/mindfs-pinned-session-projects/.test(listSource),
  "项目置顶的旧 localStorage 缓存层必须删掉 —— 它只做到「先出帧」，没做到跨设备",
);
assert.ok(
  !/readLocalProjectPins|writeLocalProjectPins/.test(listSource),
  "localStorage 读写助手必须一并删掉",
);
assert.ok(
  /\}, \[selectedRootId, selectedNodeId\]\)/.test(listSource),
  "项目置顶的拉取必须依赖 selectedRootId/selectedNodeId（切项目、切节点、切账户都要重拉）",
);

// ── 不持久化：置顶只用内存 state（决策 2026-10-05）──
// 曾经加过一层 localStorage「首帧缓存」，它是**双份真相**：服务端是权威，
// 本地又留一份永不更新的一份 —— 项目置顶当初就因为住在共享偏好里而跨账户
// 泄漏，两份真相并集等于迟早分叉。为一张几十字节的偏好表付这个代价不划算。
// 只查**代码**（pinsCode 已剥掉注释）：注释里写「为什么删掉 localStorage」是正常的。
assert.ok(
  !/localStorage|getStoredString|setStoredString|currentUser/.test(pinsCode),
  "置顶不落localStorage：偏好表几十字节，服务端有权威副本，持久化只会制造双份真相",
);
assert.ok(
  !/seedPinsFromCache/.test(appSource) && !/seedPinsFromCache/.test(listSource),
  "没有「首帧读缓存」这回事 —— 首帧空、服务端到达排一次，是**一次**跳变，不是狂跳",
);
// store 必须真的被用起来（否则上面那些「不持久化」断言会平凡成立）
assert.ok(
  /useSyncExternalStore\(subscribePins, readPins, readPins\)/.test(appSource),
  "App 必须从 store 订阅置顶，而不是自己 useState（两份状态 = 各排一次 = 狂跳）",
);
assert.ok(
  /useSyncExternalStore\(subscribePins, readPins, readPins\)/.test(listSource),
  "SessionList 也必须订阅同一个 store —— 它曾自己 useState + useEffect 拉一次",
);
assert.ok(
  !/const \[pinnedProjects, setPinnedProjects\]/.test(listSource),
  "SessionList 不该再有自己那份pinnedProjects state",
);

// ── 排序决策权只归 store（决策 2026-10-05）──
// 列表回包曾自带 pinnedKeys 参与排序，于是两个数据源都能重排列表、谁后到谁
// 说了算 —— 右侧狂跳的来源之一。现在列表只管数据。
const applyPinCalls = appSource.match(/applyPinSnapshot\(/g) || [];
assert.ok(applyPinCalls.length > 0, "列表仍要用 applyPinSnapshot 盖置顶");
assert.ok(
  !/applyPinSnapshot\([^)]*pinnedKeys/.test(appSource),
  "applyPinSnapshot 不得再收列表回包的 pinnedKeys —— 排序权只能有一个来源",
);

// 写置顶不再经过 sessionService（那条路打的是选中节点）
assert.ok(
  !appSource.includes("sessionService.setSessionPinned"),
  "会话置顶必须走 controlPath 打主节点，不能打当前选中的节点",
);
const sessionSource = fs.readFileSync(path.resolve(SRC, "services/session.ts"), "utf8");
assert.ok(
  !/async setSessionPinned/.test(sessionSource),
  "sessionService.setSessionPinned 已退役（写的是会话库的 pinned_at）",
);

console.log("pins contract ok");
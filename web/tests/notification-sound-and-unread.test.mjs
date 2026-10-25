import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

// 提示音与「完成未读」（G-KA）。
//
// 用户 2026-10-25 的诉求有三条，这里逐条钉住：
//   ① 三类事件（完成 / 需要介入 / 出错）各有明确可辨的提示音；
//   ② 提示音有专门设置入口（总开关 + 音量 + 逐事件开关）；
//   ③ 任务完成除了灭灯，还要留一个「完成未读」徽标，只有被查看才消失。
//
// 未读键必须是 `nodeId::rootId::sessionKey`：只按 sessionKey 存时，同名项目/同名 key
// 跨节点会互相点亮（这正是前几轮「未读一直不实现」的两个根因之一 —— 另一个是
// 多项目列表压根没读这份状态）。

const ROOT = path.resolve(import.meta.dirname, "..");

function transpile(relPath) {
  const sourcePath = path.resolve(ROOT, relPath);
  return {
    sourcePath,
    source: fs.readFileSync(sourcePath, "utf8"),
    code: ts.transpileModule(fs.readFileSync(sourcePath, "utf8"), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
    }).outputText,
  };
}

function makeStorage() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => { map.set(k, String(v)); },
    removeItem: (k) => { map.delete(k); },
    clear: () => map.clear(),
    _map: map,
  };
}

function loadUnread(localStorage, windowStub) {
  const { sourcePath, code } = transpile("src/shared/unreadCompleted.ts");
  const sandbox = { exports: {}, module: { exports: {} }, require: () => ({}), localStorage, window: windowStub };
  vm.runInNewContext(code, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

function makeWindowStub() {
  const listeners = new Map();
  return {
    listeners,
    addEventListener: (type, cb) => {
      if (!listeners.has(type)) listeners.set(type, new Set());
      listeners.get(type).add(cb);
    },
    removeEventListener: (type, cb) => { listeners.get(type)?.delete(cb); },
    emit: (type, event) => { for (const cb of listeners.get(type) || []) cb(event); },
  };
}

// ── 未读存储 ───────────────────────────────────────────────────────

test("未读键带节点与项目，跨节点同名会话不互相点亮", () => {
  const localStorage = makeStorage();
  const ui = loadUnread(localStorage, makeWindowStub());
  assert.equal(ui.unreadKey("pc", "proj", "s1"), "pc::proj::s1");
  assert.equal(
    ui.unreadKey("fn", "proj", "s1") === ui.unreadKey("pc", "proj", "s1"),
    false,
    "不同节点的同名会话键必须不同",
  );
  // 会话项取键三者取齐；缺 key 时不产生键（否则所有空 key 会话共用一个未读位）
  assert.equal(
    ui.unreadKeyOfSession({ _nodeId: "pc", root_id: "proj", key: "s1" }),
    "pc::proj::s1",
  );
  assert.equal(
    ui.unreadKeyOfSession({ key: "s1" }, "fallback"),
    "::fallback::s1",
    "缺 _nodeId 时用空节点段占位，避免与带节点的键串味",
  );
});

test("未读增删会持久化并通知订阅者", () => {
  const localStorage = makeStorage();
  const windowStub = makeWindowStub();
  const ui = loadUnread(localStorage, windowStub);
  const seen = [];
  const off = ui.unreadCompletedSubscribe((set) => seen.push([...set]));

  ui.unreadCompletedAdd("pc::proj::s1");
  assert.deepEqual([...seen.at(-1)], ["pc::proj::s1"], "订阅者必须立刻收到新增");
  assert.deepEqual(
    JSON.parse(localStorage.getItem("mindfs.unreadCompleted.v2")),
    ["pc::proj::s1"],
    "状态落在前端 localStorage",
  );

  ui.unreadCompletedRemove("pc::proj::s1");
  assert.deepEqual([...seen.at(-1)], [], "点开即灭");

  // 另一个标签页改了 → storage 事件是唯一的跨标签页同步信号
  localStorage.setItem("mindfs.unreadCompleted.v2", JSON.stringify(["fn::proj::s9"]));
  windowStub.emit("storage", { key: "mindfs.unreadCompleted.v2" });
  assert.deepEqual([...seen.at(-1)], ["fn::proj::s9"]);

  off();
  const before = seen.length;
  ui.unreadCompletedAdd("pc::proj::s2");
  assert.equal(seen.length, before, "退订后不再回调");
  assert.equal(windowStub.listeners.get("storage")?.size ?? 0, 0, "退订必须摘掉 storage 监听");
});

test("v1 未读键（只按 sessionKey）直接丢弃，不做迁移", () => {
  const localStorage = makeStorage();
  localStorage.setItem("mindfs.unreadCompleted", JSON.stringify(["s1", "s2"]));
  loadUnread(localStorage, makeWindowStub());
  assert.equal(
    localStorage.getItem("mindfs.unreadCompleted"),
    null,
    "v1 无从推断节点/项目，迁移等于把误标带过来",
  );
});

// ── 提示音 ─────────────────────────────────────────────────────────

function makeAudioStub() {
  const oscs = [];
  class FakeCtx {
    constructor() {
      this.state = "running";
      this.currentTime = 0;
      this.destination = {};
    }
    resume() { this.state = "running"; return Promise.resolve(); }
    createOscillator() {
      const osc = {
        type: "sine",
        _freq: 0,
        frequency: {
          setValueAtTime: (v, t) => { osc._freq = v; osc._at = t; },
        },
        connect: () => {},
        start: () => {},
        stop: () => {},
      };
      oscs.push(osc);
      return osc;
    }
    createGain() {
      return {
        gain: { setValueAtTime: () => {}, exponentialRampToValueAtTime: () => {} },
        connect: () => {},
      };
    }
  }
  return { oscs, FakeCtx };
}

function loadSound(localStorage, audio) {
  const { sourcePath, code } = transpile("src/app/useNotificationSound.ts");
  const window = {
    AudioContext: audio.FakeCtx,
    addEventListener: () => {},
    removeEventListener: () => {},
  };
  const react = {
    useCallback: (fn) => fn,
    useEffect: () => {},
    useRef: (init) => ({ current: init }),
  };
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => (id === "react" ? react : {}),
    localStorage,
    window,
  };
  vm.runInNewContext(code, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

test("三类事件各有独立音型：完成上行、介入双击、出错下行", () => {
  const localStorage = makeStorage();
  const audio = makeAudioStub();
  const sound = loadSound(localStorage, audio);
  const { play } = sound.useNotificationSound();

  const capture = (event) => {
    audio.oscs.length = 0;
    play(event);
    // 每个事件至少两个音符 —— 单音在嘈杂环境里和系统提示分不开
    return audio.oscs.map((o) => o._freq);
  };

  const complete = capture("complete");
  const intervene = capture("intervene");
  const error = capture("error");

  assert.ok(complete.length >= 2 && intervene.length >= 2 && error.length >= 2);
  assert.ok(
    complete[complete.length - 1] > complete[0],
    "完成音必须上行（收束感），不能和出错混淆",
  );
  assert.ok(error[error.length - 1] < error[0], "错误音必须下行");
  assert.equal(
    new Set([complete.join(), intervene.join(), error.join()]).size,
    3,
    "三类音型两两不同，闭着眼也分得清",
  );
  assert.ok(
    Math.max(...error) < Math.min(...complete),
    "错误音整体低于完成音（闷），这是两个维度里的音高维度",
  );
});

test("设置项真的能关掉声音：总开关与逐事件开关都生效", () => {
  const localStorage = makeStorage();
  const audio = makeAudioStub();
  const sound = loadSound(localStorage, audio);
  const { play } = sound.useNotificationSound();

  play("complete");
  assert.ok(audio.oscs.length > 0, "默认应放音");

  audio.oscs.length = 0;
  sound.saveNotificationSettings({
    ...sound.notificationSoundDefaults,
    events: { complete: false, intervene: true, error: true },
  });
  play("complete");
  assert.equal(audio.oscs.length, 0, "关掉「任务完成」后该事件静音");
  play("error");
  assert.ok(audio.oscs.length > 0, "关一个事件不能连坐其它事件");

  audio.oscs.length = 0;
  sound.saveNotificationSettings({ ...sound.notificationSoundDefaults, enabled: false });
  play("intervene");
  assert.equal(audio.oscs.length, 0, "总开关关闭后一律静音");

  // 存的是 localStorage：刷新页面后设置仍在
  assert.equal(JSON.parse(localStorage.getItem("mindfs.notify.sound")).enabled, false);
  assert.equal(sound.loadNotificationSettings().events.complete, true, "缺字段回落默认值");
});

// ── WS 接线 ────────────────────────────────────────────────────────

const NOOP_LOCAL = {
  reloadSessionForReplay: async () => {},
  getReplayTargetsForRoot: () => [],
  replayTargetsForAllRoots: () => {},
  refreshSessionRelatedFiles: () => {},
  handleSessionStreamDone: () => true,
  handleSessionStream: () => {},
  handleSlashCommandStream: () => {},
  handleSlashCommandDone: () => {},
  handleFileChanged: () => {},
  handleFileChangedBatch: () => {},
};

function loadWsHandlers(localStorage, windowStub, unread) {
  const { sourcePath, code } = transpile("src/app/realtimeWsHandlers.ts");
  const sandbox = {
    exports: {},
    module: { exports: {} },
    localStorage,
    window: windowStub,
    document: { visibilityState: "visible" },
    require: (id) => {
      if (id.includes("shared/unreadCompleted")) return unread;
      if (id.includes("realtimeWsSessionMeta")) return { createSessionMetaHandlers: () => ({}) };
      if (id.includes("realtimeWsTaskHandlers")) return { createRealtimeWsTaskHandlers: () => ({}) };
      if (id.includes("realtimeWsUserMessage")) return { handleSessionUserMessage: () => {} };
      if (id.includes("realtimeWsSessionAccepted")) return { handleSessionAccepted: () => {} };
      if (id.includes("realtimeWsRootChanged")) return { handleRootChanged: () => {} };
      if (id.includes("rootNode")) return { setRootNodeMap: () => {} };
      if (id.includes("nodeRegistry")) return { applyNodesFromServer: () => {}, syncNodesFromServer: () => {} };
      if (id.includes("appMisc")) return { mapManagedRootsToEntries: () => [], normalizeUpdateState: (v) => v };
      if (id.includes("appPath")) return { rootNodeKey: (...p) => p.join("::") };
      if (id.includes("appTask")) return { normalizeFastService: (v) => v };
      if (id.includes("appSession")) return { normalizeMode: (v) => v };
      if (id.includes("services/session")) return { dropTransientExchanges: (s) => s, isSamePendingEcho: () => false };
      return {};
    },
  };
  vm.runInNewContext(code, sandbox, { filename: sourcePath });
  return { factory: sandbox.exports.createRealtimeWsHandlers, document: sandbox.document };
}

test("session.done：非当前会话 → 完成音 + 未读点", () => {
  const localStorage = makeStorage();
  const sound = [];
  const unread = loadUnread(localStorage, makeWindowStub());
  const { factory, document } = loadWsHandlers(localStorage, makeWindowStub(), unread);
  const ctx = {
    refs: {
      currentRootIdRef: { current: "proj" },
      currentRootNodeIdRef: { current: "pc" },
      selectedSessionRef: { current: { key: "other" } },
      sessionCacheRef: { current: {} },
      sessionsRef: { current: [] },
      drawerSessionByRootRef: { current: {} },
      pendingBySessionRef: { current: {} },
      pendingRequestRef: { current: {} },
      queuedMessagesBySessionRef: { current: {} },
      queueFrozenBySessionRef: { current: {} },
      optimisticDequeuedIdsRef: { current: {} },
    },
    setters: {
      setMultiProjectSessionPending: () => {},
      setSessions: () => {},
      setDrawerSessionForRoot: () => {},
      setSelectedSession: () => {},
      setQueueVersion: () => {},
    },
    actions: {
      getNodeIdForRoot: () => "pc",
      playNotificationSound: (event) => sound.push(event),
      resolveRootForSessionKey: () => "",
      scheduleSessionListReload: () => Promise.resolve(),
      bumpCacheVersion: () => {},
      loadSessionsForRoot: () => Promise.resolve(),
    },
    values: {
      multiProjectSessionsEnabled: false,
      rootSessionKey: (r, k) => `${r}::${k}`,
      scopedRootKey: (r) => r,
      t: (k) => k,
    },
  };
  document.visibilityState = "visible";
  const handlers = factory(ctx, NOOP_LOCAL);

  handlers["session.done"]({}, { root_id: "proj", session_key: "s1" });
  assert.deepEqual(sound, ["complete"], "任务完成必须响完成音");
  assert.deepEqual(
    JSON.parse(localStorage.getItem("mindfs.unreadCompleted.v2")),
    ["pc::proj::s1"],
    "没在看的会话完成 → 标未读（这就是「灭灯 + 未读点」里的未读点）",
  );

  // 正在看的会话：音照响（用户可能正看着别处），但**不能**标未读 ——
  // 标了还得再点一次才灭，等于凭空多一个红点。
  sound.length = 0;
  ctx.refs.selectedSessionRef.current = { key: "s2" };
  handlers["session.done"]({}, { root_id: "proj", session_key: "s2" });
  assert.deepEqual(sound, ["complete"]);
  assert.deepEqual(
    JSON.parse(localStorage.getItem("mindfs.unreadCompleted.v2")),
    ["pc::proj::s1"],
    "正在看的会话不标未读",
  );

  // 页面在后台：即使「选中」还是它，人也看不见 —— 必须标未读。
  document.visibilityState = "hidden";
  handlers["session.done"]({}, { root_id: "proj", session_key: "s2" });
  assert.ok(
    JSON.parse(localStorage.getItem("mindfs.unreadCompleted.v2")).includes("pc::proj::s2"),
    "后台标签页里完成的会话必须标未读",
  );
});

test("session.error：请求没发出去要响错误音，且只对有 pending 的请求响", () => {
  const localStorage = makeStorage();
  const sound = [];
  const unread = loadUnread(localStorage, makeWindowStub());
  const { factory } = loadWsHandlers(localStorage, makeWindowStub(), unread);
  const pendingRequestRef = { current: { req1: { rootId: "proj", sessionKey: "s1" } } };
  const ctx = {
    refs: {
      currentRootIdRef: { current: "proj" },
      currentRootNodeIdRef: { current: "pc" },
      selectedSessionRef: { current: null },
      sessionCacheRef: { current: {} },
      sessionsRef: { current: [] },
      drawerSessionByRootRef: { current: {} },
      pendingBySessionRef: { current: {} },
      pendingRequestRef,
      queuedMessagesBySessionRef: { current: {} },
      queueFrozenBySessionRef: { current: {} },
      optimisticDequeuedIdsRef: { current: {} },
    },
    setters: { setMultiProjectSessionPending: () => {} },
    actions: { playNotificationSound: (e) => sound.push(e) },
    values: { scopedRootKey: (r) => r, rootSessionKey: (r, k) => `${r}::${k}`, multiProjectSessionsEnabled: false },
  };
  const handlers = factory(ctx, NOOP_LOCAL);

  handlers["session.error"]({}, { request_id: "nope" });
  assert.deepEqual(sound, [], "对不上 pending 的 error 帧不当成用户可见的失败");

  handlers["session.error"]({}, { request_id: "req1" });
  assert.deepEqual(sound, ["error"], "发送失败必须响错误音");
  assert.equal(pendingRequestRef.current.req1, undefined, "错误帧消费掉 pending");
});

// ── 需要介入 ───────────────────────────────────────────────────────

function loadMetaHandlers(localStorage, windowStub, unread) {
  const { sourcePath, code } = transpile("src/app/realtimeWsSessionMeta.ts");
  const sandbox = {
    exports: {},
    module: { exports: {} },
    localStorage,
    window: windowStub,
    require: (id) => {
      if (id.includes("appTask")) return { normalizeFastService: (v) => v };
      if (id.includes("appSession")) return { normalizeMode: (v) => v, normalizeSessionAuxFlags: (v) => v };
      return {};
    },
  };
  vm.runInNewContext(code, sandbox, { filename: sourcePath });
  return sandbox.exports.createSessionMetaHandlers;
}

test("ask_user_waiting 只在 false→true 那一下响介入音", () => {
  const localStorage = makeStorage();
  const sound = [];
  const unread = loadUnread(localStorage, makeWindowStub());
  const createSessionMetaHandlers = loadMetaHandlers(localStorage, makeWindowStub(), unread);
  const sessionCacheRef = {
    current: {
      "proj::s1": { key: "s1", root_id: "proj", exchanges: [] },
    },
  };
  const ctx = {
    refs: {
      boundSessionByRootRef: { current: {} },
      selectedSessionRef: { current: null },
      sessionCacheRef,
      sessionsRef: { current: [{ key: "s1", aux_flags: {} }] },
    },
    setters: {
      setDrawerSessionForRoot: () => {},
      setSelectedSession: () => {},
      setSessions: (fn) => { ctx.refs.sessionsRef.current = fn(ctx.refs.sessionsRef.current); },
    },
    actions: {
      bumpCacheVersion: () => {},
      loadSessionsForRoot: () => Promise.resolve(),
      playNotificationSound: (e) => sound.push(e),
      refreshTaskWorktree: () => {},
      refreshTasksForRelatedSession: () => {},
      scheduleMultiProjectSessionReload: () => {},
      updateSessionRelatedWorktreeForKey: () => {},
    },
    values: {
      multiProjectSessionsEnabled: false,
      rootSessionKey: (r, k) => `${r}::${k}`,
      scopedRootKey: (r) => r,
    },
  };
  const handlers = createSessionMetaHandlers(ctx, { refreshSessionRelatedFiles: () => {} });
  const frame = (waiting) => ({
    root_id: "proj",
    session: { key: "s1", updated_at: "2026-10-25T00:00:00Z", aux_flags: { ask_user_waiting: waiting } },
  });

  handlers["session.meta.updated"]({}, frame(false));
  assert.deepEqual(sound, [], "一开始没在等，不该响");

  handlers["session.meta.updated"]({}, frame(true));
  assert.deepEqual(sound, ["intervene"], "轮到用户了 → 介入音");

  // 同一个提问会随 meta 广播重复下发：重复的 true 不能再响。
  handlers["session.meta.updated"]({}, frame(true));
  assert.deepEqual(sound, ["intervene"], "重复广播不连响");

  handlers["session.meta.updated"]({}, frame(false));
  handlers["session.meta.updated"]({}, frame(true));
  assert.deepEqual(sound, ["intervene", "intervene"], "答完再问是新的介入，必须再响");
  assert.equal(
    ctx.refs.sessionsRef.current[0].aux_flags.ask_user_waiting,
    true,
    "列表项 aux 就地更新（aux 不改 updated_at，增量拉带不回来）",
  );
});

// ── 选中即已读 ─────────────────────────────────────────────────────

test("清未读挂在选择漏斗上，覆盖列表点击/深链/看板跳转", () => {
  const source = fs.readFileSync(path.resolve(ROOT, "src/app/useSessionSelection.ts"), "utf8");
  const removeAt = source.indexOf("unreadCompletedRemove(");
  const bindAt = source.indexOf("setBoundSessionForRoot(targetRoot, key)");
  assert.ok(removeAt > 0, "handleSelectSession 必须清未读");
  assert.ok(
    removeAt < bindAt,
    "清除要发生在绑定之前 —— 绑定后列表已重渲染，会先闪一下红点",
  );
  assert.match(
    source,
    /unreadCompletedRemove\(unreadKey\(nextNode, targetRoot, key\)\)/,
    "键必须用 handleSelectSession 算出的同一个 nextNode/targetRoot/key",
  );
});

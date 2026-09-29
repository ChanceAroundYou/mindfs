import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

// 「没选中任何会话时新建并运行，不自动跳到新会话」的回归。
//
// 症状：主区停在空面板「从右侧会话列表选择一个会话开始对话」，agent 明明在跑。
// 根因：新建会话时 handleSendMessage 只把乐观键 setBoundSessionForRoot 成绑定键，
// 渲染的主区空态只看 selectedSession（App.tsx: mainView==="chat" && !selectedSession
// 分支），selectedSession 仍是 null —— 于是主区永远不显示这条会话。
// 修法：sendSessionKey 为空（=真的在新建）时顺手把乐观条目选进主区。
//
// 纯逻辑测试，不依赖浏览器/网络：appSession.ts 的两个判据函数。
// require 用桩顶掉（./appTask 与 ../services/session 会拖进 React），跟
// cross-node-replying-state.test.mjs 同一套写法。

const sourcePath = path.resolve(import.meta.dirname, "../src/app/appSession.ts");
const source = fs.readFileSync(sourcePath, "utf8");
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

const { shouldAutoSelectNewSession, isSessionShownInMain } = sandbox.exports;
assert.equal(typeof shouldAutoSelectNewSession, "function", "appSession must export shouldAutoSelectNewSession");
assert.equal(typeof isSessionShownInMain, "function", "appSession must export isSessionShownInMain");

const draft = { key: "pending-1", session_key: "pending-1", root_id: "mindfs" };
const base = {
  selectedSession: null,
  mainView: "chat",
  interactionMode: "main",
  draftItem: draft,
};

// ── 1. 这就是本 bug 的形状：没选中任何会话 + chat 态 + 抽屉没开 → 必须自动选中 ──
assert.equal(
  shouldAutoSelectNewSession(base),
  true,
  "a new conversation started with nothing selected must land in the main panel",
);

// ── 2. 已选中别的会话：维持原行为（续聊/排队都走 sendSessionKey 分支，不能抢焦点）──
assert.equal(
  shouldAutoSelectNewSession({ ...base, selectedSession: { key: "existing" } }),
  false,
  "an already-selected session keeps the focus; the new one must not steal it",
);

// ── 3. 文件态下不把人从正在看的文件里拽走，仍走抽屉 ──────────────────────────
assert.equal(
  shouldAutoSelectNewSession({ ...base, mainView: "files" }),
  false,
  "sending from the files view must not yank the user out of the file",
);

// ── 4. 抽屉已开：焦点另有归属（浮动会话框），不抢 ────────────────────────────
assert.equal(
  shouldAutoSelectNewSession({ ...base, interactionMode: "drawer" }),
  false,
  "with the session drawer open the focus belongs to the drawer",
);

// ── 5. 乐观条目造不出来（toSessionItem 返回 null）就没有可渲染的东西，别选 ──────
assert.equal(
  shouldAutoSelectNewSession({ ...base, draftItem: null }),
  false,
  "nothing to select when the optimistic item could not be built",
);

// ── 6. 选进主区后，isBoundInMain 必须认这个 pending-* 键 ────────────────────
// 否则 sendSessionKey 仍是 undefined，会被判成「不在主区」又把抽屉顶开，
// 自动跳转刚做出来就被自己 undo。
assert.equal(
  isSessionShownInMain({
    selectedKey: "pending-1",
    sendSessionKey: undefined,
    tempKey: "pending-1",
    interactionMode: "main",
  }),
  true,
  "the just-selected optimistic key counts as shown-in-main",
);

// ── 7. 已选中别的会话：sendSessionKey 为空 ≠ 在主区，抽屉照旧顶开 ────────────
assert.equal(
  isSessionShownInMain({
    selectedKey: "existing",
    sendSessionKey: undefined,
    tempKey: "pending-1",
    interactionMode: "main",
  }),
  false,
  "an unrelated selected session leaves the drawer to open",
);

// ── 8. 续聊已有会话（sendSessionKey 有值且等于选中键）：在主区，别顶抽屉 ──────
assert.equal(
  isSessionShownInMain({
    selectedKey: "real-1",
    sendSessionKey: "real-1",
    tempKey: "",
    interactionMode: "main",
  }),
  true,
  "continuing a session already shown in the main panel must not open the drawer",
);

// ── 9. 抽屉态一律不在主区（与旧判据一致，别把这层语义改掉）────────────────
assert.equal(
  isSessionShownInMain({
    selectedKey: "real-1",
    sendSessionKey: "real-1",
    tempKey: "",
    interactionMode: "drawer",
  }),
  false,
  "drawer mode is never the main panel",
);

// ── 10. 什么都没选中：不在主区 ─────────────────────────────────────────────
assert.equal(
  isSessionShownInMain({
    selectedKey: "",
    sendSessionKey: undefined,
    tempKey: "",
    interactionMode: "main",
  }),
  false,
  "no selection means not in the main panel",
);

// ── 11. 接线：判据必须真的挂在 handleSendMessage 的新建分支上 ───────────────
// 上面十条只锁判据本身；这两条钉住 App.tsx 没把函数拆出来却忘了调用
// （拆成纯函数就是为了能测，但拆了不接 = bug 原样存在）。
const app = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/App.tsx"),
  "utf8",
);
assert.match(
  app,
  /shouldAutoSelectNewSession\(\{\s*selectedSession: selectedSessionRef\.current,\s*mainView: mainViewRef\.current,\s*interactionMode: interactionModeRef\.current,\s*draftItem,/,
  "the new-session branch must consult shouldAutoSelectNewSession with the live refs",
);
assert.match(
  app,
  /setSelectedSession\(draftItem\);/,
  "the optimistic new session must be selected so the main panel stops showing the empty state",
);
// 主区空态的判据没变：仍是「chat 态且没选中会话」，所以自动选中是唯一出路。
assert.match(
  app,
  /if \(mainView === "chat" && !selectedSession\) \{/,
  "the main empty state is still driven purely by selectedSession",
);

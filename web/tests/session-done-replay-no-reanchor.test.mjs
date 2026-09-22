import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const doneBody = app.slice(
  app.indexOf('case "session.done": {'),
  app.indexOf('case "session.user_message": {'),
);

assert.ok(doneBody.length > 0, 'the "session.done" handler should be found in App.tsx');

// 服务端在每次 session.ready 之后都会补发一条 replay=true 的 session.done
// （h.completed 只在新回合开始时清空，回合结束后一直留着），而 restoreActiveSession
// 自己又会发 session.ready。若 replay 的 done 也触发 reloadSessionForReplay，就闭合为
//   done → restoreActiveSession → session.ready → ReplayPending → done
// 的自持环。2026-09-13 实测：空闲时 18 次/秒、session.done / session.ready /
// ?latest=20 严格 1:1:1，且永不衰减。
assert.match(
  doneBody,
  /payload\?\.replay !== true\s*&&[\s\S]{0,80}?getReplayTargetsForRoot\(rootID\)\.includes\(sessionKey\)/,
  "a replayed done must not trigger reloadSessionForReplay (it closes the replay loop)",
);

// 真·回合结束的 done 仍然要重锚定，否则 done 后不再用持久化窗口替换缓存，
// 瞬时尾巴会以 seq=0 形式重复追加在窗口后面。
assert.match(
  doneBody,
  /void reloadSessionForReplay\(rootID, sessionKey\)/,
  "a live (non-replay) done must still re-anchor the window",
);

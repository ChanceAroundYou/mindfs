import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/app/useRealtimeEvents.ts", import.meta.url), "utf8");
const svc = readFileSync(new URL("../src/services/session.ts", import.meta.url), "utf8");
// 2026-09 App.tsx 拆分：WS 事件处理器整块搬到 app/useRealtimeEvents.ts，契约随文件走。
// 更早的一轮拆分已把 `switch (event.type) { case "x": … }` 变成
// `"x": (event, payload) => { … }` 的 handler map，切片锚点用后者。
const doneStart = app.indexOf('"session.done": (event: any, payload: any) => {');
const doneBody = app.slice(
  doneStart,
  app.indexOf('"session.user_message": (event: any, payload: any) => {'),
);
const clip = (src, startMarker, endMarker) => {
  const s = src.indexOf(startMarker);
  assert.ok(s >= 0, `missing marker: ${startMarker}`);
  return src.slice(s, src.indexOf(endMarker, s));
};
const finishBody = clip(
  app,
  "const handleSessionStreamDone = (",
  "const handleSessionStream = (payload: any) => {",
);

assert.ok(doneStart >= 0 && doneBody.length > 0, 'the "session.done" handler should be found in useRealtimeEvents.ts');

// ── 契约一：服务端不再补发 done 回执（连同 `completed` 表一起删） ───────────────────
// 守的那起事故是真的：done → restoreActiveSession → session.ready → ReplayPending → done
// 自持环，2026-09-13 实测 18 次/秒、`session.done / session.ready / ?latest=20` 严格 1:1:1；
// 2026-10-06 真机重跑再次复现（80 条 done / 78 次 ?latest=20，≈8 次/秒）。
// 根因不在客户端少一个守卫，在**服务端多一条回执**：`completed` 表 + `done(replay:true)`。
// 它想表达的「你挂上来时这一轮早就结束了」本来就是多余的 ——
//   · 真实结束的 done 用 `liveOnly=false` 广播，**重放中的客户端也在收件人里**；
//   · `broadcastSessionDone` 在广播 done 之前已经推过 pending 列表（该会话已不在其中），
//     客户端的「在回复」状态由此收敛；
//   · 客户端对 replay 回执的两处分支本来就都是**空操作**：不放提示音、不重锚定。
// 见 docs/session-streaming-rework.md §6。
assert.doesNotMatch(
  doneBody,
  /payload\?\.replay/,
  "done 回执上的 replay 标记已废除（连带服务端 completed 表），不得回潮",
);

// ── 契约二：回合收尾只有一个出口，且**先装窗口、再退尾巴** ─────────────────────────
// 曾经这里有两套判断共存、顺序还相反：done 处理器先无条件清瞬时尾巴，再用
// `isSessionStreaming` 猜「要不要重锚定」—— 而那个判据在监听者里恒为 true（状态机在
// 派发**之后**才更新，见契约三），于是正常回合的重锚定恒被跳过，尾巴却已经被清 ⇒
// 正在完成的正文瞬间消失、只剩已落盘的用户行。2026-10-06 真机复现确认。
//
// 现在：能重锚定的会话**不在这里清尾巴** —— 窗口快照一到，载入时的唯一组装规则
// （composeLoadedExchanges，`pending=false` ⇒ 不接回 seq=0）就是唯一的退役时机。
assert.match(
  finishBody,
  /willReanchor\s*\?\s*base\s*:\s*dropTransientExchanges\(base\)/,
  "能重锚定的会话必须把尾巴交给窗口组装退役，不能提前清（提前清 + 重锚定被拦下 = 这一轮永远不见）",
);
assert.match(
  finishBody,
  /getReplayTargetsForRoot\(rootID\)\.includes\(sessionKey\)/,
  "「要不要重锚定」只能由 replay 目标集合判定，不能用 isSessionStreaming 之类的时序代理",
);
assert.match(
  finishBody,
  /void reloadSessionForReplay\(rootID, sessionKey\)/,
  "a live done must still re-anchor the window",
);
assert.doesNotMatch(
  finishBody,
  /isSessionStreaming/,
  "收尾路径不得再用 isSessionStreaming 猜「还在不在流」—— 真正的判据是队列续跑（hasQueuedContinuation，本函数上方已 return）",
);
assert.doesNotMatch(
  doneBody,
  /isSessionStreaming|reloadSessionForReplay/,
  "done 处理器只做簿记：重锚定的唯一决策点在 handleSessionStreamDone 里，处理器不得再自行判断一遍",
);

// ── 契约三：状态机必须在派发**之前**更新 ─────────────────────────────────────────
// `emit` 同步调用监听者；`updateActiveStreamState` 若排在它后面，监听者读到的就是
// 「上一帧」的状态。契约一守的环、契约二守的交接失败，都建立在这条之上。
{
  const body = clip(svc, "if (sessionKey) {\n      this.updateActiveStreamState", "const handlers = this.handlers.get(sessionKey);");
  assert.match(
    body,
    /this\.updateActiveStreamState\(type, sessionKey, nextPayload\);\s*\}\s*this\.emit\(\{ type, sessionKey, payload: nextPayload \}\);/,
    "emitDecrypted 必须先 updateActiveStreamState 再 emit（否则监听者读到上一帧的流状态）",
  );
}

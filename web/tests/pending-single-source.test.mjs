import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

// pending 只有一个真相：`multiProjectPendingByKey`。**不再往会话对象上写 pending。**
//
// 背景（G-AY）：会话的「在不在回复」原先在前端有五份投影 —— 缓存 / 抽屉 / 选中 /
// pendingBySessionRef / multiProjectPendingByKey。只有列表蓝灯那份每 5s 从
// `/api/replying-sessions` 对账；其余四份只有 WS `session.done` 一个出口，断连/重绑
// 竞态丢了那条事件就**永久卡在 true**：输入框一直显示停止符号、查看器一直「正在思考」、
// 用量面板不出现。
//
// 第一版修法是给那四处补一条对账（`clearStalePending` + 一个 effect）。那是打补丁：
// 第二份状态还在，就还得再写一套「怎么同步第二份状态」的代码，而且每加一处存储就漏一处。
// 2026-10-07 改成**纯派生**：会话对象上不再存 pending，读的时候从唯一真相表取。
// 于是没有第二份状态，也就没有「两份不同步」这回事。
//
// 这个测试钉的就是那条不变量（源码守卫，不需要浏览器/网络）：
//   1. 唯一写入点是 setMultiProjectSessionPending；
//   2. App.tsx / useRealtimeEvents.ts 里**不存在** `pending: true|false` 这种往会话
//      对象上写字面量的地方；
//   3. 三个读点（getSessionSnapshot / resolvePendingForSession / rootSessionIndicators）
//      都从 multiProjectPendingByKey（或其同步镜像 multiProjectPendingRef）派生。

const webRoot = path.resolve(import.meta.dirname, "..");

function read(rel) {
  return fs.readFileSync(path.join(webRoot, rel), "utf8");
}

function where(hits) {
  return hits.map((h) => h.line).join(", ");
}

/** 逐行扫一个文件，返回匹配的行号 + 行内容。整行注释不算（注释里引代码是常态）。 */
function scanLines(file, re) {
  const out = [];
  const lines = file.split("\n");
  for (let i = 0; i < lines.length; i += 1) {
    const text = lines[i].trim();
    if (text.startsWith("//") || text.startsWith("*") || text.startsWith("/*")) continue;
    if (re.test(lines[i])) out.push({ line: i + 1, text });
  }
  return out;
}

const app = read("src/App.tsx");
const realtime = read("src/app/useRealtimeEvents.ts");
const appSession = read("src/app/appSession.ts");

// ── 1. 补丁已拔掉：clearStalePending 与 setSelectedPendingByKey 都不该再存在 ─────────
{
  assert.equal(
    /clearStalePending/.test(appSession),
    false,
    "appSession.ts 不该再有 clearStalePending —— 那是给「第二份 pending」做对账的补丁",
  );
  assert.equal(
    /setSelectedPendingByKey/.test(app + realtime),
    false,
    "setSelectedPendingByKey 是「往 selected 会话对象上写 pending」的入口，必须删干净",
  );
}

// ── 2. 唯一写入点 ──────────────────────────────────────────────────────────────
{
  const writers = scanLines(app + realtime, /setMultiProjectPendingByKey\(|multiProjectPendingRef\.current\s*=/);
  assert.ok(writers.length > 0, "找不到 multiProjectPendingByKey 的写入路径");
  // 写入必须收敛在 setMultiProjectSessionPending（定义处）与轮询合并（refreshMultiProjectReplyingSessions）
  // 两个已知点；别处再冒出来就是又长了一份状态。
  const setter = app.indexOf("const setMultiProjectSessionPending = useCallback(");
  assert.ok(setter >= 0, "找不到 setMultiProjectSessionPending");
  assert.match(
    app.slice(setter, setter + 1200),
    /multiProjectPendingRef\.current = next;[\s\S]*setMultiProjectPendingByKey\(next\)/,
    "setMultiProjectSessionPending 必须同步更新 ref 再 setState（幂等判断读 ref）",
  );
}

// ── 3. 不变量：不再往会话对象上写 pending 字面量 ─────────────────────────────────
{
  for (const [name, src] of [
    ["src/App.tsx", app],
    ["src/app/useRealtimeEvents.ts", realtime],
  ]) {
    const writes = scanLines(src, /pending:\s*(true|false)\b/);
    assert.equal(
      writes.length,
      0,
      `${name} 里仍有往会话对象上写 pending 的地方（应为 0）。今天 ${writes.length} 处：${where(writes)}`,
    );
  }
}

// ── 4. 三个读点都派生自唯一真相 ────────────────────────────────────────────────
{
  // getSessionSnapshot：快照里的 pending 从表里取，不从 drawer/selected/cache 读。
  const snapStart = app.indexOf("const getSessionSnapshot = useCallback(");
  assert.ok(snapStart >= 0, "找不到 getSessionSnapshot");
  const snap = app.slice(snapStart, snapStart + 2000);
  assert.match(
    snap,
    /const pending = !!multiProjectPendingByKey\[rootSessionKey\(rootId, key\)\];/,
    "getSessionSnapshot 的 pending 必须从 multiProjectPendingByKey 派生",
  );

  // resolvePendingForSession：读同步镜像 ref（不把 state 拉进 restoreActiveSession 的依赖）。
  const resolveStart = app.indexOf("const resolvePendingForSession = useCallback(");
  assert.ok(resolveStart >= 0, "找不到 resolvePendingForSession");
  assert.match(
    app.slice(resolveStart, resolveStart + 1200),
    /multiProjectPendingRef\.current\[key\]/,
    "resolvePendingForSession 必须读 multiProjectPendingRef（唯一真相的同步镜像）",
  );

  // rootSessionIndicators（FileTree 的项目尾点）：也从表里派生。
  const indStart = app.indexOf("const rootSessionIndicators = useMemo(");
  assert.ok(indStart >= 0, "找不到 rootSessionIndicators");
  assert.match(
    app.slice(indStart, indStart + 1200),
    /multiProjectPendingByKey\)\.some\(/,
    "rootSessionIndicators 的 pending 必须从 multiProjectPendingByKey 派生",
  );
}

// ── 5. 回合收尾仍然收敛：session.done 必须清唯一真相 ────────────────────────────
// 纯派生之后，「清 pending」只剩一个动作。它要还在 —— 否则这一轮跑完灯就一直亮着。
{
  const doneStart = realtime.indexOf("const handleSessionStreamDone = (rootID: string, sessionKey: string): boolean => {");
  assert.ok(doneStart >= 0, "找不到 handleSessionStreamDone");
  const done = realtime.slice(doneStart, doneStart + 4000);
  assert.match(
    done,
    /setMultiProjectSessionPending\(rootID, sessionKey, false\)/,
    "handleSessionStreamDone 必须清 multiProjectSessionPending（唯一真相）",
  );
}

// ── 6. 轮询（服务端真值）必须还在，且按节点增量合并 ─────────────────────────────
// 唯一真相表本身只由两处更新：WS 乐观写 + 这个轮询用服务端真值覆盖。
// 轮询没了，表就成了第二份「只靠事件维护」的状态 —— 又回到丢 done 就卡住的老路。
{
  assert.match(
    app,
    /mergeReplyingStateByNode\(\s*multiProjectPendingRef\.current/,
    "refreshMultiProjectReplyingSessions 必须用 mergeReplyingStateByNode 按节点增量合并",
  );
  assert.match(
    appSession,
    /export function mergeReplyingStateByNode\(/,
    "appSession.ts 必须仍然导出 mergeReplyingStateByNode",
  );
}

// ── 7. 流内指示器也必须读唯一真相（G-AY 补完，2026-10-09） ───────────────────────
// useSessionStream 的本地 `isStreaming` 是最后一份漏网的投影：它只由 WS 事件清
// （done / message_done / error），**没有轮询对账**。丢一条 done ⇒ 列表蓝灯与
// 停止键被轮询救回、流里「正在生成」永久不灭（连带最后一条的时间戳一直藏着）。
// 治法与 G-AY 同：不让它独立存在 —— 用唯一真相 sessionPending 收闸门。
{
  const hook = read("src/hooks/useSessionStream.ts");
  const retStart = hook.indexOf("return {\n    timeline: settledTimeline");
  assert.ok(retStart >= 0, "找不到 useSessionStream 的返回块");
  assert.match(
    hook.slice(retStart, retStart + 900),
    /isStreaming:\s*isStreaming\s*&&\s*!!sessionPending,/,
    "useSessionStream 导出的 isStreaming 必须**恰好**是 isStreaming && !!sessionPending " +
      "（收闸门）—— 裸导出本地标记 = 又一份只靠 WS 事件维护的 pending 投影",
  );

  // 查看器的「正在生成」只能由 pending 决定显不显示；一般化地钉住 isAwaiting 的来源。
  const viewer = read("src/components/session/SessionViewer.tsx");
  assert.match(
    viewer,
    /const isAwaiting = !!\(session as any\)\?\.pending;/,
    "SessionViewer 的 isAwaiting 必须派生自 session.pending（唯一真相）",
  );
  assert.equal(
    /\(isAwaiting \|\| isStreaming\)/.test(viewer),
    false,
    "流内指示器的显示条件不得再 OR 上本地 isStreaming —— 那是没有对账的那一份",
  );
}

console.log("pending-single-source.test.mjs: OK");

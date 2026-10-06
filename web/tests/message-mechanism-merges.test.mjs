// 「该合并 / 该砍掉」的**删除契约测试** —— 每条对应 `docs/message-mechanisms.md` §3.2 / §3.4 的一项。
//
// 层次说明（别把这一层当行为测试）：本文件全部是**源码结构断言**，钉的是「重复机制已经不存在」，
// 不是「行为正确」。它的价值只有两个：
//   ① 让「该砍什么」变成可判定的 —— 每条现在**红**，砍完才绿；
//   ② 防止砍掉之后被人「顺手加回来」。
// 所以每条都配一句「为什么这算重复」的依据。能写成行为测试的一律写在
// `session-core-unit.test.mjs` 或 `session-overlay-unit.test.mjs` 里，本文件不替代它们。
//
// 凡是能**自动推导**的判据（死帧、重复点计数）就不硬编码清单 —— 硬编码的清单会随着代码
// 演化变成谎话，而推导出来的不会。
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { execSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const webRoot = path.resolve(fileURLToPath(new URL("..", import.meta.url)));
const repoRoot = path.resolve(webRoot, "..");
const srcRoot = path.join(webRoot, "src");

/** 递归收集 web/src 下所有源文件。 */
function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) out.push(...walk(full));
    else if (/\.(ts|tsx)$/.test(name)) out.push(full);
  }
  return out;
}
const srcFiles = walk(srcRoot);
/**
 * 扫源码找标识符。**先剥注释** —— 否则「某个被废除的写法不得回潮」这类断言会被
 * 解释它的注释自己触发（这些测试的红/绿因此变成谎话）。踩过两次：`inferredSeq`、
 * `forceNoTruncate`。剥注释后，注释里自由地写「曾经用 X，为什么删掉」不会干扰判据。
 */
// **行数保持不变**：块注释整体替换成等量换行、行注释只删内容。
// 否则 where() 报的行号是「剥注释后的偏移」，会把人指到错误的位置（实测踩过）。
const stripComments = (src) =>
  src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ""))
    .replace(/\/\/[^\n]*/g, "");
/** @returns {Array<{file:string,line:number,text:string}>} */
function scanSrc(re) {
  const hits = [];
  for (const file of srcFiles) {
    stripComments(readFileSync(file, "utf8")).split("\n").forEach((text, i) => {
      if (re.test(text)) hits.push({ file: path.relative(webRoot, file), line: i + 1, text: text.trim() });
    });
  }
  return hits;
}
const countOf = (re) => scanSrc(re).length;
const where = (hits, n = 4) =>
  hits.slice(0, n).map((h) => `${h.file}:${h.line}`).join(" · ");

// ─────────────────────────────────────────────────────────────────────────────
// ② 「瞬时 vs 持久」必须**只有一处实现**，且两条路径都走它
// 依据：冲突①②。`dropTransientExchanges` 用谓词、`mergeSessionExchanges` 内联 `seq > 0`，
// 于是「同一行该不该留」有两把尺，去留取决于先跑哪条路径。
//
// 原判据写成「谓词本身该消失（类别由数据结构表达）」—— 那是**两泳道**的架构目标，不是
// 当前这一步能安全交付的（需要把瞬时时序从行数组里搬出来，牵动乐观回声 / ask 卡 / 队列续轮 /
// 子会话 / 双挂载五处边界）。**能真正防止 bug 回归**的不变量是这个：
// 判定只有一处实现，且两条对外路径都调用它 —— 只要有人再内联一次，这条就红。
// 两泳道仍记在 `docs/message-mechanisms.md` §3.2 ② 作为目标形态。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·②】瞬时/持久的判定只能有一处实现，两条路径都必须走它", () => {
  const file = readFileSync(path.join(srcRoot, "services/session.ts"), "utf8");
  // ① 谓词存在且被两条路径调用
  const mergeStart = file.indexOf("export function mergeSessionExchanges(");
  const composeStart = file.indexOf("export function composeLoadedExchanges(");
  assert.ok(mergeStart >= 0 && composeStart >= 0, "找不到两个组装函数");
  const mergeBody = file.slice(mergeStart, composeStart);
  const composeEnd = file.indexOf("export function isTransientExchange(");
  const composeBody = file.slice(composeStart, composeEnd > 0 ? composeEnd : composeStart + 3000);
  assert.match(mergeBody, /isTransientExchange\(/, "mergeSessionExchanges 必须走共用谓词，不得内联 seq 判定");
  assert.match(composeBody, /isTransientExchange\(/, "composeLoadedExchanges 必须走共用谓词");

  // ② 交换行域内不得再出现内联的「是否持久行」判定
  const inline = scanSrc(/seq \|\| 0\) *[><]=? *0|seq \|\| 0\) !== 0/).filter(
    (h) => !h.file.endsWith("services/session.ts"),
  );
  assert.equal(
    inline.length,
    0,
    `session.ts 之外仍在自行判定「是不是持久行」—— 那就是第二把尺。今天 ${inline.length} 处：${where(inline)}`,
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// ③ 工具卡去重只能有一处
// 依据：冲突③ —— `windowToolCallIds`（渲染前让位）与 `dedupeToolCards`（时间线内保留首个）
// 是同一个 concern 的两套判据（callId 集合 vs 首个保留）。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·③】工具卡去重不得有两套判据（windowToolCallIds 应消失）", () => {
  const hits = scanSrc(/windowToolCallIds/);
  assert.equal(hits.length, 0, `工具卡去重两处实现（见 §3.4 冲突③）。今天仍在：${where(hits)}`);
});

// ─────────────────────────────────────────────────────────────────────────────
// ④ 内容比对的**三个补丁**必须消失（2026-10-06 收窄：不是「内容比对全删」）
// 依据：原本判成「内容比对整类该消失」，动手时被 fixture 证伪 —— 下面两种形状**完全一样**：
//   · 窗口有本轮的持久行 + 缓存有一条瞬时行，该瞬时行是**新的流式内容** → 必须保留；
//   · 同一形状，但该瞬时行是那条持久行的**拷贝** → 必须让位。
// 二者只能靠内容区分（`session-window.test.mjs` 的 ② 与 ⑦ 就是这一对）。所以内容比对是必要的，
// 该删的是它的三个补丁：
//   · `windowTailTexts` —— 只扫窗口最新 3 行，更早的本轮行认不出；
//   · `renderedPersistedTexts` —— 上面那份语料的扩展，语料该由「本轮」界定而不是由行数；
//   · `OVERLAY_DUP_MIN_CHARS` / `OVERLAY_TAIL_ROWS` —— 长度地板与行数上限。
// 保留并在 `session-window.test.mjs` 里正向钉住的：`normalizeOverlayText`（去空白，服务端补
// `"\n\n"`）、`windowUserCounts`（瞬时 user 行的计数消抵，跨端丢事件的兜底）。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·④】内容比对的三个补丁必须消失（语料按本轮界定，不按行数/长度）", () => {
  for (const id of [
    "windowTailTexts",
    "renderedPersistedTexts",
    "OVERLAY_DUP_MIN_CHARS",
    "OVERLAY_TAIL_ROWS",
  ]) {
    const hits = scanSrc(new RegExp(`\\b${id}\\b`));
    assert.equal(hits.length, 0, `${id} 仍在 → 内容比对的补丁还没拔掉。${where(hits)}`);
  }
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑤ 「这个会话在不在回复」只能有一个真值源
// 依据：冲突⑤ —— 9 个机制、五处存储（cache/drawer/selected/pendingBySessionRef/multiProjectPendingRef）。
// 目标是「服务端 pendingSessions 为唯一源，前端只保留展示缓存」。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑤】清除 pending 必须覆盖全部五处存储（残缺清除 = 状态漂移）", () => {
  // 2026-10-06 收窄：原本判成「9 个机制、5 处存储该合并成 1 处」。核对后确认**该合并的是
  // 清除路径，不是存储** ——
  //   · `pending` 是**派生展示状态**（真值在服务端 `pendingSessions`），五处（缓存 / 抽屉 /
  //     选中 / pendingBySessionRef / multiProjectPendingByKey）都是它的投影；
  //   · 投影多份本身可以接受，**不可接受的是清除时漏掉某几处**：漏了就是「灯不灭」「一直显示
  //     在回复」。实测有两处正是这样（发送取消 / 发送失败各只清了 2 处），已改为走清除编排者。
  // 该钉的就是这条：清除必须一次覆盖五处。
  const file = readFileSync(path.join(webRoot, "src/App.tsx"), "utf8");
  const start = file.indexOf("const clearLocalPendingForSession = useCallback(");
  assert.ok(start >= 0, "找不到清除编排者 clearLocalPendingForSession");
  // 函数体到下一个顶层 useCallback 为止
  const nextTop = file.indexOf("\n  const ", start + 10);
  const body = file.slice(start, nextTop > 0 ? nextTop : start + 4000);
  for (const place of [
    "pendingBySessionRef",
    "sessionCacheRef",
    "setSelectedSession",
    "setDrawerSessionForRoot",
    "setMultiProjectSessionPending",
  ]) {
    assert.match(body, new RegExp(place), `清除编排者漏了「${place}」—— 那一处会永久停在 pending`);
  }
  // 残缺清除的指纹：只把选中标记与小蓝灯一起清掉（缓存与抽屉仍停在 pending）
  const partial = scanSrc(/setSelectedPendingByKey\([^)]*false\);\s*\n\s*setMultiProjectSessionPending\(/);
  assert.equal(
    partial.length,
    0,
    `存在「只清两处」的残缺清除（灯不灭的直接来源）。今天 ${partial.length} 处：${where(partial)}`,
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑥ 乐观回声的「确认」只能有一个判据
// 依据：冲突⑥ —— 同一条乐观行（`pending_ack`）被三种匹配键清：`content+timestamp`、
// `role+content`、全量清。**这条做成自动推导**：数「清 pending_ack 的位置」，
// 今天实测 7 处（按本判据计 —— 过滤掉了类型声明与 `=== true` 的读点）。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑥】乐观回声的两条判据各只能有一处实现", () => {
  // 依据：冲突⑥。原本这个形状散在 7 处：1 处创建、2 处**无条件清**（App.tsx 与
  // useRealtimeEvents 各写了一份完全一样的局部 `clearPendingAck`）、2 处**认领**
  // （按 content+timestamp 匹配并把 seq 转正）。
  //
  // 真重复的是后两个家族，各自收敛成一个导出函数：
  //   · 清空 → `services/session.ts` 的 `settlePendingAcks`
  //   · 认领 → 同文件的 `isSamePendingEcho`
  // 判据按**形状**数，不按 `pending_ack` 这个词数（读点与创建点本来就该保留）。
  const clearShape = scanSrc(/if \(exchange\?\.pending_ack !== true\) return exchange;/);
  assert.equal(
    clearShape.length,
    1,
    `「无条件清」只能有一处实现（在 settlePendingAcks 里）。今天 ${clearShape.length} 处：${where(clearShape)}`,
  );
  const claimShape = scanSrc(/pending_ack !== true\) return false;/);
  assert.equal(
    claimShape.length,
    1,
    `「认领谓词」只能有一处实现（在 isSamePendingEcho 里）。今天 ${claimShape.length} 处：${where(claimShape)}`,
  );
  const inlineClear = scanSrc(/pending_ack === true\s*\?/);
  assert.equal(
    inlineClear.length,
    0,
    `不得再内联「匹配后清空」的形状（要用共用函数）。今天 ${inlineClear.length} 处：${where(inlineClear)}`,
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑦ exchange_aux：两种语义**各自正确**，但拼接必须幂等
// 依据（2026-10-06 更正）：一度判定「统一成按 seq 覆盖」，核对调用方后确认**那是错的** ——
//   · `restoreActiveSession` 走覆盖：窗口回包是那些 seq 的**权威快照**；
//   · `appendExchangeAuxDelta` 走拼接：增量路径里同一个 seq 会**陆续追加**新的 thought/tool
//     项，覆盖会把先到的丢掉（它只有一个生产调用方：`session.ts` 的增量同步）。
// 真正的缺陷只是拼接**不幂等** —— 重投递（游标漂移、同步重跑、重锚定重复投递）会把同一张
// 工具卡再加一遍。所以这里钉的不是「删掉谁」，而是「各自守住自己的契约」。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑦】aux 拼接必须幂等，且窗口路径不得用它（覆盖语义）", () => {
  const file = readFileSync(path.join(srcRoot, "services/session.ts"), "utf8");
  const start = file.indexOf("export function appendExchangeAuxDelta(");
  assert.ok(start >= 0, "找不到 appendExchangeAuxDelta");
  const body = file.slice(start, file.indexOf("\n}", start));
  assert.match(
    body,
    /const seen = new Set\(/,
    "拼接必须去重（幂等）：否则增量重投递会把同一张工具卡再加一遍",
  );
  // 窗口路径必须走覆盖：不得出现「用 append 打窗口回包」的写法
  const app = readFileSync(path.join(webRoot, "src/App.tsx"), "utf8");
  assert.doesNotMatch(
    app,
    /appendExchangeAuxDelta\(/,
    "窗口路径（restoreActiveSession）必须用按 seq 覆盖，不得用拼接",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑧ 落盘淘汰只能有一个判据
// 依据：冲突⑧ —— `mergeSessionExchanges`「加载只增不减」与 `toPersistentSession`「写时从头部淘汰」
// 相反；而窗口态的 `forceNoTruncate` 又绕开上限，同一会话两种落盘体积。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑧】写盘淘汰不得有绕过上限的例外（forceNoTruncate 应消失）", () => {
  const hits = scanSrc(/forceNoTruncate/);
  assert.equal(hits.length, 0, `淘汰判据被绕过（见 §3.4 冲突⑧）。今天仍在：${where(hits)}`);
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑨ compact 边界规则必须删除
// 依据：冲突⑨ —— 该规则的**前提是假的**：注释称「compact 是服务端历史的重置点」，而服务端交换
// JSONL 是 append-only（全仓唯一 `os.Remove` 在两处 `DeleteSession`），compact 只写一条
// CompactNotice aux。行为层已另有一条红测试证明它会丢掉「本轮中途 compact 时的在途行」。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑨】composeLoadedExchanges 不得再按 compact 边界切瞬时行", () => {
  const file = readFileSync(path.join(srcRoot, "services/session.ts"), "utf8");
  const start = file.indexOf("export function composeLoadedExchanges(");
  assert.ok(start >= 0, "找不到 composeLoadedExchanges");
  const body = file.slice(start, file.indexOf("\n}", start));
  assert.doesNotMatch(
    body,
    /lastCompact/,
    "compact 边界规则仍在，而它依据的前提（服务端会重置历史）是假的（见 §3.4 冲突⑨）",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑨-c compact 时的客户端清理点：依据也是假的，且会误伤本轮
// 依据：`useRealtimeEvents` 的 `compact_notice` 分支注释写着「服务端历史整个重置了…
// 此刻没有在途回合，不存在误伤」—— **两句都不成立**：
//   · 服务端交换 JSONL 是 append-only（唯一 `os.Remove` 在两处 `DeleteSession`）；
//   · `compact_notice` 是**轮内**流事件（`handleSessionStream` 的一个 case），当时正有在途回合。
// 后果：compact 一到就把该会话所有 seq=0 行丢掉 —— 连同**本轮 compact 之前**那些还没落盘的
// 内容，以及刚追加的那条 compact 通知自己。
//
// 为什么用结构断言而不是行为测试：这个点在 hook 内部（`useRealtimeEvents`），不可 import。
// 删除它是对的：一轮结束 / reset 两处清理已覆盖陈旧行，加载时 `inFlight=false` 也是兜底。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑨c】compact 分支不得清瞬时行（它的依据为假，且会丢掉本轮前半段）", () => {
  // 判据只看 **compact 分支内部**：`useRealtimeEvents` 里另有一处 `dropTransientExchanges`
  // 是合法的（收尾时对「不会重锚定的会话」清尾巴），不能一起数进来 —— 那条是必要清理。
  const file = path.join(srcRoot, "app/useRealtimeEvents.ts");
  const src = stripComments(readFileSync(file, "utf8"));
  const start = src.indexOf('case "compact_notice"');
  assert.ok(start >= 0, "找不到 compact_notice 分支");
  const next = src.indexOf("case ", start + 10);
  const body = src.slice(start, next > 0 ? next : undefined);
  assert.doesNotMatch(
    body,
    /dropTransientExchanges/,
    "compact 分支仍在清瞬时行：服务端历史并未重置（交换 JSONL append-only），" +
      "而 compact 是**轮内**事件 ⇒ 会把本轮 compact 之前还没落盘的内容一起丢掉",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑫ 不得存在「发出但无人消费」的帧
// **自动推导**：从 `WSResponse{...}` 块里提取帧类型（今天 25 个），逐个查 web/src 是否引用。
// 今天 2 个是死的：session.answer_question.accepted、session.repointed。
// 注意别用「全仓 Type: 字面量」去提 —— 那会把 agent 事件类型（message_chunk 等）与
// Web Push 类型（session.ask_user）也算进来，产生 5 个假阳性。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑫】服务端发出的 WS 帧必须都有前端消费者", () => {
  const raw = execSync(
    'grep -rhA3 "WSResponse{" server/internal/api/ || true',
    { cwd: repoRoot, encoding: "utf8", shell: "/bin/bash" },
  );
  const types = new Set(
    [...raw.matchAll(/Type:\s*"([a-z_][a-z_.]*)"/g)].map((m) => m[1]),
  );
  assert.ok(types.size >= 20, `帧类型提取异常（只提到 ${types.size} 个）——判据本身坏了，别当成通过`);

  const consumed = new Set(
    srcFiles
      .map((f) => readFileSync(f, "utf8"))
      .join("\n")
      .match(/"([a-z_][a-z_.]*)"/g)
      ?.map((s) => s.slice(1, -1)) ?? [],
  );
  const dead = [...types].filter((t) => !consumed.has(t));
  assert.deepEqual(dead, [], `以下帧服务端会发、前端零引用（见 §3.4 冲突⑫）：${dead.join(", ")}`);
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑮ 四组「同一件事写两遍」各自收敛到一处
// **自动推导**：扫 web/src 数重复点，不硬编码位置。
// 今天的实测计数写在断言消息里，便于一眼看出收敛进度。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑮a】附件的文本 token 只能有一个组装点", () => {
  const hits = scanSrc(/`\[file: \$\{/);
  assert.ok(
    hits.length <= 1,
    `[file: ] 的组装写在了多处（改一处必漏其余）。今天 ${hits.length} 处：${where(hits)}`,
  );
});

test("【红·⑮b】plan 文本前缀只能有一个所有者（检测/剥离/拼装同源）", () => {
  const hits = scanSrc(/"\/plan|'\/plan/);
  assert.ok(
    hits.length <= 1,
    `同一个 /plan 前缀被多处各自解析/拼装（改格式必漏）。今天 ${hits.length} 处：${where(hits)}`,
  );
});

test("【红·⑮c】selection 的去重必须**按会话**且**按完整 selection** 判等", () => {
  // 2026-10-06 更正：原先判成「`contextCache` 与 `buildClientContext` 是同一判定的两处实现」，
  // 核对后**不成立** —— 两者是不同操作：`buildClientContext` 是**构建**（camel→snake），
  // `compactContext` 是**去重**（同一个 selection 不每条消息都重复带，省字节）。
  // 服务端 `buildUserPrompt` 只用**当前这条**消息的 selection，所以去重是刻意的设计，不是补丁。
  //
  // 该钉的是它的**边界**（这两条错了就会丢上下文或误判相等）：
  const file = readFileSync(path.join(srcRoot, "services/session.ts"), "utf8");
  assert.match(
    file,
    /if \(sessionKey\) \{\s*\n\s*const prev = this\.contextCache\.get\(sessionKey\);/,
    "去重必须**按会话**：跨会话共用一条缓存会让新会话的第一条消息就丢掉 selection",
  );
  assert.match(
    file,
    /return `\$\{filePath\}:\$\{startLine\}:\$\{endLine\}:\$\{text\}`;/,
    "判等键必须包含 file+行号+文本；漏任一项会把「同一个文件的不同选区」误判成没变",
  );
});

test("【红·⑮d】context_window 两处必须分工明确（按 exchange 优先，会话级只兜底）", () => {
  // 2026-10-06 更正：原先判成「两处补同一字段，删一处」。核对后**不成立** —— 两者**数据源不同**：
  //   · `buildAssistantTimeline` 贴的是**该 exchange 自己的** `context_window`（每轮真值）；
  //   · `applySessionContextWindow` 用**会话级** `context_window` 兜底，只在行上缺字段时填。
  // 删掉兜底会让「服务端早期没记 per-exchange 的那类行」永远没有 token 徽标。
  const file = readFileSync(path.join(srcRoot, "hooks/useSessionStream.ts"), "utf8");
  assert.match(
    file,
    /contextWindow: ex\.context_window,/,
    "按 exchange 的那一路（优先级更高）missing",
  );
  assert.match(
    file,
    /if \(\s*item\.contextWindow\?\.totalTokens &&\s*item\.contextWindow\?\.modelContextWindow\s*\) \{\s*\n\s*return items;/,
    "会话级兜底必须**只在缺失时**填 —— 不得覆盖按 exchange 的那份真值",
  );
});

// ─────────────────────────────────────────────────────────────────────────────
// ⑬ 无界缓冲必须加上限
// 依据：冲突⑬ —— `pendingStreams`（无 handler 时缓存事件，只 push 不设阈值）与
// `ReplyingList`（仅 userShell 有 256KB 截断）都没有真正的容量上限。
// ─────────────────────────────────────────────────────────────────────────────
test("【红·⑬】pendingStreams 必须有上限，且 push 路径要**用上**它", () => {
  const file = readFileSync(path.join(srcRoot, "services/session.ts"), "utf8");
  // 判据分两段：常量存在 **且** push 处真的按它裁剪 —— 只看常量会被「定义了没用」骗过。
  assert.match(
    file,
    /const PENDING_STREAM_LIMIT = \d+;/,
    "缺少容量上限常量：长回合 + 无人订阅会无声堆积（见 §3.4 冲突⑬）",
  );
  const pushStart = file.indexOf("const queued = this.pendingStreams.get(sessionKey)");
  assert.ok(pushStart >= 0, "找不到 pendingStreams 的 push 路径");
  const pushBody = file.slice(pushStart, pushStart + 600);
  assert.match(
    pushBody,
    /PENDING_STREAM_LIMIT/,
    "push 路径没有按上限裁剪（只定义常量不算修）",
  );
});

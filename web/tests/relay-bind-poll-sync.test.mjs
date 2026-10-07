import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

/**
 * relay 绑定轮询测试同步的契约守卫（`Scope: G-AX`）。
 *
 * 用户症状：`TestManagerPollTerminalBindStatusStopsPolling` 偶发失败，报
 *   `pending code did not clear after expired bind status`，耗时正好 5.00s。
 * 本机（12GB 内存 + swap 压力 + 并发 agent）复现过；单独跑 30/30 全绿。
 *
 * 根因：**状态落地晚于 channel 发送，两者之间没有同步关系**。
 * 本文件的 mock transport 在**返回响应之前**就把 URL 送进 `requests`
 * （`requests <- req.URL.String()`），而 poller 要等响应返回之后才走
 * `onFinished` 更新 `Status()`（`manager.go` 的 `pollLoop`：
 * `m.pendingCode = ""; m.lastError = status`）。
 * 原实现从 `requests` 读到请求就**立刻**查 `Status()`，不是 `"expired"` 就
 * `continue` 去读下一个请求 —— 而 poller 收到 expired 之后已经 `return` 了，
 * 再没有下一个请求，于是卡到 5s 超时。负载越高越必然输。
 *
 * 这一层钉的是形状：这是「静默退化」型改动 —— 测试全绿、短跑看不出问题，
 * 只有持续跑或负载高时才炸，而它炸的时候看起来像产品 bug。
 */

const root = path.resolve(import.meta.dirname, "..");
const source = fs.readFileSync(
  path.join(root, "../server/internal/relay/service_test.go"),
  "utf8",
);

// ── ① 修复必须存在：收到请求后要等状态落地，而不是读一次就 continue ─────────
const staleCheck = /if status\.LastError != "expired" \{\s*continue\s*\}/;
assert.doesNotMatch(
  source,
  staleCheck,
  "the bind-status test must not check Status() exactly once and continue — " +
    "status settles AFTER the channel send, so a single check reads a stale value",
);

// 等待循环必须存在（`for time.Now().Before(deadline)` 或等价的轮询）。
assert.match(
  source,
  /for time\.Now\(\)\.Before\(deadline\)/,
  "the bind-status test must wait for the status to settle instead of sampling it once",
);
assert.match(
  source,
  /time\.Sleep\(2 \* time\.Millisecond\)/,
  "the settle-wait must poll rather than block on a channel that will never fire again",
);

// ── ② 通道必须无缓冲 ──────────────────────────────────────────────────────
// 带缓冲时 poller 的发送不阻塞，它会抢在测试观察之前跑完整个 poll
// （expired → onFinished → 清空 PendingCode），`expected initial pending code`
// 就会偶发失败。无缓冲让 poller 停在发送上，与 ① 的「等状态落地」互补。
const terminalTest = source.slice(
  source.indexOf("func TestManagerPollTerminalBindStatusStopsPolling"),
  source.indexOf("func TestManagerPollTerminalBindStatusSettlesAfterChannelSend"),
);
assert.doesNotMatch(
  terminalTest,
  /requests := make\(chan string,\s*\d+\)/,
  "the requests channel must be unbuffered — a buffered channel lets the poller " +
    "clear PendingCode before the test observes it",
);
assert.match(
  terminalTest,
  /requests := make\(chan string\)/,
  "the requests channel must be declared unbuffered",
);

// ── ③ 顺序假设必须被确定性复现 ────────────────────────────────────────────
// 这条测试把「状态晚于 channel 发送」从偶发变成必然：mock 在送进 channel 之后
// 再睡 150ms 才返回响应，于是读到请求的那一刻 poller 一定还没更新状态。
assert.match(
  source,
  /func TestManagerPollTerminalBindStatusSettlesAfterChannelSend/,
  "the ordering assumption must be pinned by a deterministic reproduction test",
);
// 以 sleep 为锚点往回看：它必须紧跟在 channel 发送之后（而不是隔了几千字符）。
const sleepIdx = source.indexOf("time.Sleep(150 * time.Millisecond)");
assert.ok(sleepIdx > 0, "the reproduction must delay the response after the channel send");
assert.match(
  source.slice(Math.max(0, sleepIdx - 300), sleepIdx),
  /requests <- req\.URL\.String\(\)/,
  "the reproduction must delay the response until after the channel send",
);
// 它必须断言「读到请求时状态还没落地」—— 这正是原实现失败的那一刻。
assert.match(
  source,
  /precondition failed: status already settled/,
  "the reproduction must assert the status has NOT settled yet right after the channel send",
);

// ── ③ 行为契约：expired 之后 PendingCode 必须被清空 ───────────────────────
assert.match(
  source,
  /expected pending code to clear after expired status/,
  "the 'expired clears the pending code' contract must still be asserted",
);

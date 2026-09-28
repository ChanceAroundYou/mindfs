import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 2026-09-28 实测 bug：发完消息、agent 还在跑（已经出了几行）时，用户自己那条消息
// 从对话列表消失；点「同步」也刷不回来；等 agent 跑完，它又自己出现了。
//
// 根因是 user 行落盘太晚：回合开始时就把「即将获得」的预测 seq 广播出去，真正
// AddExchangeForAgentAt 却在 runtime.SendMessage 返回之后。整轮执行期间服务器上
// 唯一的那份用户消息只是 StreamHub 的内存 pending 态，GetWindow 的 maxSeq 只数
// 已落盘行，永远看不到它。客户端却已按 seq 认定它持久化 → SessionViewer overlay
// 的「seq<=latestSeq 即让位给窗口」规则在任意一次重锚定时把它丢掉；而同步的
// localTransientTail 只捞 seq===0 的瞬时行，捞不到它。
//
// 修复：user 行在回合开始前就落盘，广播的 seq 从预测变成事实。
//
// 这是一条**源码顺序**守卫：只有真正的前移才能满足它，补偿式的补丁改动过不了。

const sessionGo = readFileSync(
  new URL("../../server/internal/api/usecase/session.go", import.meta.url),
  "utf8",
);

// ── 1. user 行必须在真正开跑之前落盘 ──────────────────────────────────────
// SendMessage 主体（排除同名 helper 声明）：写入点要早于 agent 真正 SendMessage 的点。
const bodyStart = sessionGo.indexOf("func (s *Service) SendMessage(");
assert.ok(bodyStart >= 0, "SendMessage should be found");
const bodyEnd = sessionGo.indexOf("\nfunc ", bodyStart + 1);
assert.ok(bodyEnd > bodyStart, "SendMessage body should be bounded");
const body = sessionGo.slice(bodyStart, bodyEnd);

const persistAt = body.indexOf("persistUserTurnExchange(");
const agentSendAt = body.indexOf("runtime.SendMessage(");
assert.ok(persistAt >= 0, "user 行应通过 persistUserTurnExchange 落盘");
assert.ok(agentSendAt >= 0, "runtime.SendMessage 应存在");
assert.ok(
  persistAt < agentSendAt,
  "user 行必须在 runtime.SendMessage 之前落盘（否则整轮执行期间窗口读不到它）",
);

// ── 2. 回合末尾不得再补写 user 行 ──────────────────────────────────────────
// 补写会让同一轮出现两条 user 行。
assert.ok(
  !/AddExchangeForAgentAt\(\s*agentExchangeCtx,\s*current,\s*"user"/.test(body),
  "回合末尾不得再写 user 行：它已在回合开始前落盘",
);
assert.ok(
  !/persistCommandTurn\([\s\S]{0,400}"user"/.test(sessionGo),
  "persistCommandTurn 只该补助手行与 aux，不得再写 user 行",
);

// ── 3. seq 广播值必须来自真实写入，不得是预测 ──────────────────────────────
// 预测式写法（base+1）与真值（persistUserTurnExchange 的返回值）不能并存。
assert.ok(
  !/UserExchangeSeq:\s*baseExchangeSeq\s*\+\s*1/.test(body),
  "UserExchangeSeq 必须是落盘返回的真值，不能是 baseExchangeSeq+1 的预测",
);
assert.ok(
  /UserExchangeSeq:\s*userExchangeSeq/.test(body),
  "UserExchangeSeq 应直接下发改动后的 userExchangeSeq",
);

// ── 4. pendingUser 旁路已随「晚落盘」一起消失 ───────────────────────────────
// 它的存在前提是「回合执行中读接口拿不到该行」；前提没了，旁路只会造成重复渲染。
const httpGo = readFileSync(
  new URL("../../server/internal/api/http.go", import.meta.url),
  "utf8",
);
assert.ok(
  !/pendingUser\.Seq\s*=\s*0/.test(httpGo),
  "sessionResponse 不得再把 user 行降级成 seq=0（那是「唯一副本还没落盘」的补丁）",
);
assert.ok(
  !/GetPendingUserExchange\(/.test(httpGo),
  "http 读接口不得再从 StreamHub 取未落盘的 user 行",
);

const streamHubGo = readFileSync(
  new URL("../../server/internal/api/stream_hub.go", import.meta.url),
  "utf8",
);
assert.ok(
  !/func \(h \*StreamHub\) GetPendingUserExchange\(/.test(streamHubGo),
  "GetPendingUserExchange 已无调用方，应删除",
);
assert.ok(
  !/func cloneUserExchange\(/.test(streamHubGo),
  "cloneUserExchange 只为把未落盘的 pending blob 转成 exchange，应删除",
);

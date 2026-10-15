// 隔离浏览器复现：child stream 不能劫持父会话的抽屉/绑定（vitest 内运行）。
// 触发：MINDFS_E2E=1 npm test -- tests/e2e-stream-ownership.test.mjs
// 未设该变量时跳过 —— 普通 `npm test` 不受影响。
//
// 场景（复现「子代理结果混在主结果里」）：
//  1. 经 WS 建一个免费 shell command 会话并发一轮 echo（拿真实 key，不走 UI 花钱路径）。
//  2. 浏览器打开该会话，从 composer 再发一条（走 UI，经乐观回声+accepted 路径）。
//  3. 等回合结束，断言第二条落进了同一抽屉（binding 没被 stream 拐走）。
//  4. 刷新页面，断言持久化重建与刷新前一致。
import { test, onTestFinished } from "vitest";
import {
  ISO,
  isoUp,
  isoAccount,
  launchBrowser,
  login,
  openSession,
  sendFromComposer,
  dumpTimelineStable,
  readExchanges,
  readBlocks,
  sleep,
} from "./e2e/harness.mjs";
import assert from "node:assert/strict";

const ENABLED = process.env.MINDFS_E2E === "1";

/** 经 WS 建免费 shell 会话并跑一轮，返回 key（与 harness.runTurn 同形，但 type=command）。 */
async function runShellTurn(content) {
  const { uid } = isoAccount();
  const clientId = `e2e-shell-${Date.now()}`;
  const ws = new WebSocket(
    `ws://127.0.0.1:${ISO.port}/mindfs/ws?client_id=${clientId}&user=${uid}`,
  );
  let sessionKey = "";
  let done = false;
  await new Promise((res, rej) => {
    ws.onopen = res;
    ws.onerror = () => rej(new Error("WS 握手失败"));
    setTimeout(() => rej(new Error("WS 连接超时")), 10000);
  });
  ws.onmessage = (m) => {
    try {
      const o = JSON.parse(m.data);
      if (o.type === "session.user_message" && o.payload?.session_key) {
        sessionKey = o.payload.session_key;
      }
      if (o.type === "session.accepted" && o.payload?.session_key && !sessionKey) {
        sessionKey = o.payload.session_key;
      }
      if (o.type === "session.stream" && o.payload?.session_key && !sessionKey) {
        sessionKey = o.payload.session_key;
      }
      if (o.type === "session.meta.updated" && o.payload?.session?.key && !sessionKey) {
        sessionKey = o.payload.session.key;
      }
      if (o.type === "session.done") done = true;
    } catch { /* 非 JSON 帧忽略 */ }
  };
  ws.send(JSON.stringify({
    id: `e2e-shell-${Date.now()}`,
    type: "session.message",
    payload: {
      root_id: "proj",
      content,
      type: "command",
      agent: "shell",
      shell: "bash",
    },
  }));
  const deadline = Date.now() + 60000;
  while (!done && Date.now() < deadline) await sleep(500);
  ws.close();
  assert.ok(sessionKey, "没拿到 session_key");
  assert.ok(done, "60s 内未收到 session.done");
  return sessionKey;
}

async function waitSettled(page, timeoutS, label) {
  for (let i = 0; i < timeoutS; i += 1) {
    await sleep(1000);
    const s = await page.ev(`(() => {
      const text = document.body.innerText || "";
      return { waiting: text.includes("正在生成") || text.includes("已发送，等待响应") };
    })()`);
    if (!s.waiting) return;
  }
  throw new Error(`${label}：${timeoutS}s 未结束`);
}

test.skipIf(!ENABLED)("E2E：stream 所有权隔离（隔离实例）", async () => {
  assert.ok(await isoUp(), `隔离实例未就绪：${ISO.base}`);
  const key = await runShellTurn("echo parent-marker-123");
  const before = await readExchanges(key);
  assert.ok(before.length >= 2, `首轮落盘条目不足：${before.length}`);

  const page = await launchBrowser({ tag: "verify-stream" });
  onTestFinished(() => page.close());
  await login(page);
  await openSession(page, key);
  await sleep(3000);
  const hasInput = await page.ev(
    `!!document.querySelector('[data-onboarding=message-input]')`,
  );
  assert.ok(hasInput, "composer 不可用");
  const shown = await dumpTimelineStable(page);

  // 第二次发送（走 UI）：binding 若被拐走，这里会发到错误会话或新建会话。
  await sendFromComposer(page, "echo parent-marker-456");
  await waitSettled(page, 90, "第二回合");
  const after = await dumpTimelineStable(page);
  assert.ok(
    after.total > shown.total,
    `第二条消息未落进同一抽屉 before=${shown.count}/${shown.total} after=${after.count}/${after.total}`,
  );

  await page.ev(`location.reload()`);
  await sleep(8000);
  const reloaded = await dumpTimelineStable(page);
  assert.equal(reloaded.count, after.count, "刷新后条目数漂移");
  assert.equal(reloaded.total, after.total, "刷新后正文总量漂移");

  // 落盘为准：两条 echo 都在，且没有异 key 内容混入。
  // 正文载体是 blocks（方案 D1 后 exchanges[].content 不再写正文）。
  const blocks = await readBlocks(key);
  const blockText = JSON.stringify(blocks);
  assert.ok(blockText.includes("parent-marker-123"), "首轮内容丢失");
  assert.ok(blockText.includes("parent-marker-456"), "第二轮内容丢失");
  console.log("e2e-stream-ownership: OK");
}, 300000);

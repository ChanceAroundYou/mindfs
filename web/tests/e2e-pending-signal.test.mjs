// E2E：pending 信号收敛（跑在**隔离实例**上，见 scripts/mindfs-iso.sh）。
//
// 为什么这一层不能省：用户报的 bug（对话完成后仍显示正在思考 + 停止键）是
// **跨模块接线 + 真实时序 + 真后端回包** 的问题，三层单测都够不到。
// 本文件钉的是「agent 行落盘后 N 秒内 pending 信号必须消失」这个最终用户可见的行为。
//
// 触发：MINDFS_E2E=1 node --test tests/e2e-pending-signal.test.mjs
// 未设该变量时整文件跳过 —— 普通 `npm test` 不受影响。
//
// 成本：一轮真实回合实测 $0.112。本文件跑两轮（一轮建会话、一轮被观察），约 $0.22/次。
import test from "node:test";
import assert from "node:assert/strict";
import {
  ISO,
  isoUp,
  runTurn,
  sendFromComposer,
  launchBrowser,
  login,
  openSession,
  readExchanges,
  sleep,
} from "./e2e/harness.mjs";

const ENABLED = process.env.MINDFS_E2E === "1";
const SKIP = ENABLED ? false : "设 MINDFS_E2E=1 才跑；需要隔离实例：bash scripts/mindfs-iso.sh start";

/** pending 信号消失的超时阈值（宽松值，不跟具体修复方式绑死）。 */
const PENDING_CLEAR_TIMEOUT_MS = 30_000;

/** 读「还在不在回复」的两个可见信号：尾部等待点 + 输入框停止键。 */
async function pendingSignals(page) {
  return page.ev(`(() => {
    const text = document.body.innerText || "";
    const waitingText = text.includes("正在生成") || text.includes("已发送，等待响应");
    const pulsing = [...document.querySelectorAll('div')].some((d) => {
      const s = d.getAttribute('style') || '';
      return s.includes('pulse') && d.children.length <= 2 && (d.innerText || '').length < 30;
    });
    const btns = [...document.querySelectorAll('button')];
    const stopBtn = btns.find((b) => {
      const s = (b.getAttribute('style') || '');
      return s.includes('239,68,68') || s.includes('239, 68, 68');
    });
    return { waitingText, pulsing, stopBtn: !!stopBtn };
  })()`);
}

/** 检查 pending 信号是否已消失。 */
function assertPendingCleared(signals, label) {
  assert.ok(
    !signals.waitingText,
    `${label}：仍显示「正在生成」或「已发送，等待响应」`,
  );
  assert.ok(
    !signals.pulsing,
    `${label}：仍有尾部等待点（pulse 动画）`,
  );
  assert.ok(
    !signals.stopBtn,
    `${label}：仍有停止键`,
  );
}

test("E2E：pending 信号收敛（隔离实例）", { skip: SKIP }, async (t) => {
  assert.ok(await isoUp(), `隔离实例未就绪：${ISO.base} —— 先 bash scripts/mindfs-iso.sh start`);

  // ── 夹具：一轮真回合建出会话 ──────────────────────────────────────────────
  const created = await runTurn({ content: "只回复两个字：收到" });
  const key = created.key;
  assert.match(created.assistantText, /收到/, `第一轮没有得到助手的「收到」，帧：${created.frames.join(",")}`);

  const page = await launchBrowser({ tag: "pending-e2e" });
  t.after(() => page.close());
  await login(page);
  await openSession(page, key);

  // ── 场景 1：冷启动 —— 会话已完成，不该有任何 pending 信号 ─────────────────
  await t.test("冷启动：会话完成后无 pending 信号", async () => {
    await sleep(8000); // 跑过两个轮询周期
    const signals = await pendingSignals(page);
    assertPendingCleared(signals, "冷启动");
  });

  // ── 场景 2：发一轮，等它落盘（= done 之后），pending 信号必须消失 ──────────
  await t.test("agent 行落盘后 30s 内 pending 信号消失", async () => {
    const agentRowsBefore = (await readExchanges(key)).filter(
      (e) => e.role === "agent" || e.role === "assistant",
    ).length;
    await sendFromComposer(page, "回复三个字：知道了");

    // 等这一轮落盘
    const deadline = Date.now() + 240_000;
    let landed = false;
    while (Date.now() < deadline) {
      const rows = await readExchanges(key).catch(() => []);
      if (rows.filter((e) => e.role === "agent" || e.role === "assistant").length > agentRowsBefore) {
        landed = true;
        break;
      }
      await sleep(700);
    }
    assert.ok(landed, "这一轮没在 240s 内落盘");

    // 落盘后 30s 内 pending 信号必须消失
    const clearDeadline = Date.now() + PENDING_CLEAR_TIMEOUT_MS;
    let cleared = false;
    while (Date.now() < clearDeadline) {
      const signals = await pendingSignals(page);
      if (!signals.waitingText && !signals.pulsing && !signals.stopBtn) {
        cleared = true;
        break;
      }
      await sleep(1000);
    }
    const finalSignals = await pendingSignals(page);
    assert.ok(
      cleared,
      `agent 行落盘后 ${PENDING_CLEAR_TIMEOUT_MS / 1000}s 内 pending 信号未消失：${JSON.stringify(finalSignals)}`,
    );
  });

  // ── 场景 3：WS 断连丢 done 之后，pending 信号必须自己收敛 ─────────────────
  await t.test("WS 断连 20s 后 pending 信号收敛", async () => {
    // 记录所有 WS 实例，便于中途掐断（模拟「断连丢事件」）
    await page.send("Page.addScriptToEvaluateOnNewDocument", {
      source: `(() => {
        window.__ws = [];
        const Orig = window.WebSocket;
        window.WebSocket = function (...a) {
          const s = new Orig(...a);
          window.__ws.push(s);
          return s;
        };
        window.WebSocket.prototype = Orig.prototype;
        window.WebSocket.CONNECTING = 0; window.WebSocket.OPEN = 1;
        window.WebSocket.CLOSING = 2; window.WebSocket.CLOSED = 3;
      })()`,
    });

    const agentRowsBefore = (await readExchanges(key)).filter(
      (e) => e.role === "agent" || e.role === "assistant",
    ).length;
    await sendFromComposer(page, "回复三个字：知道了（WS 断连测试）");
    await sleep(2500); // 让这一轮跑起来

    // 掐断 WS 20s：这一轮的 session.done 必然丢
    await page.ev(`(() => { (window.__ws||[]).forEach(s => { try { s.close(); } catch {} }); return (window.__ws||[]).length; })()`);
    await sleep(20_000);

    // 等这一轮落盘
    const deadline = Date.now() + 240_000;
    let landed = false;
    while (Date.now() < deadline) {
      const rows = await readExchanges(key).catch(() => []);
      if (rows.filter((e) => e.role === "agent" || e.role === "assistant").length > agentRowsBefore) {
        landed = true;
        break;
      }
      await sleep(700);
    }
    assert.ok(landed, "WS 断连测试：这一轮没在 240s 内落盘");

    // 落盘后 30s 内 pending 信号必须消失（即使 WS 断连丢过 done）
    const clearDeadline = Date.now() + PENDING_CLEAR_TIMEOUT_MS;
    let cleared = false;
    while (Date.now() < clearDeadline) {
      const signals = await pendingSignals(page);
      if (!signals.waitingText && !signals.pulsing && !signals.stopBtn) {
        cleared = true;
        break;
      }
      await sleep(1000);
    }
    const finalSignals = await pendingSignals(page);
    assert.ok(
      cleared,
      `WS 断连后 agent 行落盘，但 ${PENDING_CLEAR_TIMEOUT_MS / 1000}s 内 pending 信号未消失：${JSON.stringify(finalSignals)}`,
    );
  });
});

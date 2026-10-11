// E2E：两个 pending 症状的复现 / 回归护栏（跑在**隔离实例**上，见 scripts/mindfs-iso.sh）。
//
// 症状 1：「完成回答但是还显示正在思考」—— done 后 SessionViewer 的等待指示器不消失。
//   指示器渲染条件：`isAwaiting`（= session.pending；G-AY 后不再 OR 本地 isStreaming）
//   （session/SessionViewer.tsx:3008），
//   文案 `session.generating`（正在生成...）/ `session.sentWaiting`（已发送，等待响应...）。
// 症状 2：「正在运行但是会话列表灯不亮」—— 回合进行中 SessionList 的回复点不亮。
//   渲染条件：`session.pending`（session/SessionList.tsx:1800），aria-label `sessionList.replying`（正在回复）。
//
// 为什么只有这一层能给证据：两个都是**真实时序**现象（done 与 pending 清理的竞态），
// 单测层和源码契约层都够不到。
//
// 触发：MINDFS_E2E=1 node --test tests/e2e-pending-indicators.test.mjs
// 未设该变量时整文件跳过 —— 普通 `npm test` 不受影响。
//
// 成本：两轮真实回合，约 $0.22/次。
import test from "node:test";
import assert from "node:assert/strict";
import {
  ISO,
  isoUp,
  isoAccount,
  runTurn,
  readExchanges,
  launchBrowser,
  login,
  openSession,
  sendFromComposer,
  sleep,
} from "./e2e/harness.mjs";

const ENABLED = process.env.MINDFS_E2E === "1";
const SKIP = ENABLED ? false : "设 MINDFS_E2E=1 才跑；需要隔离实例：bash scripts/mindfs-iso.sh start";

// 中英两个 locale 都要匹配 —— 隔离实例的 locale 取决于浏览器语言。
const REPLYING_SEL = '[aria-label="正在回复"], [aria-label="Replying"]';

async function listReplyingCount(page) {
  return page.ev(`document.querySelectorAll('${REPLYING_SEL}').length`);
}

async function viewerIndicator(page) {
  return page.ev(`(() => {
    const txt = document.body.innerText || '';
    return {
      generating: txt.includes('正在生成') || txt.includes('Generating'),
      sentWaiting: txt.includes('已发送，等待响应') || txt.includes('Sent, waiting'),
    };
  })()`);
}

// 服务端的「这一轮在不在跑」真值源 —— 比 CDP 抓 WS 帧可靠（实测 framesRx 恒空）。
// 字段是 camelCase（`sessionKey`），不是 wire 上的 `session_key`。
async function serverReplying(uid) {
  const r = await fetch(`${ISO.base}/api/replying-sessions?root=proj&user=${uid}`);
  const d = await r.json();
  return (d.sessions || []).map((s) => s.sessionKey || "");
}

// openSession 的固定等待不够：首次加载应用 + 会话可能超过 waitMs，
// 此时输入框还没挂上（实测 no-input）。轮询到它出现为止。
async function waitForInput(page, timeoutMs = 25000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const n = await page.ev(`document.querySelectorAll('[data-onboarding=message-input]').length`);
    if (n > 0) return true;
    await sleep(300);
  }
  return false;
}

async function waitForServerReplying(uid, key, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if ((await serverReplying(uid)).includes(key)) return true;
    await sleep(300);
  }
  return false;
}

test("E2E：pending 指示器在回合结束后消失、运行中列表灯亮（隔离实例）", { skip: SKIP }, async (t) => {
  assert.ok(await isoUp(), `隔离实例未就绪：${ISO.base} —— 先 bash scripts/mindfs-iso.sh start`);
  const { uid } = isoAccount();

  // 夹具：一轮真回合建出会话（空项目里 DOM 层建不出第一个会话）。
  const created = await runTurn({ content: "只回复两个字：收到" });
  const key = created.key;

  const page = await launchBrowser({ tag: "pending-ind" });
  try {
    await login(page);
    await openSession(page, key);
    assert.ok(await waitForInput(page), "输入框未出现（会话未打开或应用未加载完）");

    // 发一条新消息（走 UI，因此覆盖乐观回声那条路径），不等待。
    await sendFromComposer(page, "再回复两个字：好的");

    // ── 轮询：服务端 replying 列表 + UI 灯 ────────────────────────────────
    let sawServerReplying = false;
    let sawListReplying = false;
    const deadline = Date.now() + 90000;
    while (Date.now() < deadline) {
      const srv = await serverReplying(uid);
      if (srv.includes(key)) sawServerReplying = true;
      if ((await listReplyingCount(page)) > 0) sawListReplying = true;
      if (sawServerReplying && !srv.includes(key)) break; // 回合已结束
      await sleep(400);
    }
    assert.ok(sawServerReplying, `服务端从未报告该会话在回复（回合没跑起来？key=${key}）`);

    // 给前端渲染时间（done → 清 pending → 重渲染）。
    await sleep(3000);

    // ── 症状 1：done 后指示器应该消失 ─────────────────────────────────────
    const ind = await viewerIndicator(page);
    assert.equal(ind.generating, false, "回合结束后仍显示「正在生成」");
    assert.equal(ind.sentWaiting, false, "回合结束后仍显示「已发送，等待响应」");

    // ── 症状 2 反向：done 后会话列表灯也应该灭 ────────────────────────────
    assert.equal(await listReplyingCount(page), 0, "回合结束后会话列表仍显示「正在回复」");

    // ── 症状 2 正向：回合运行中灯亮过 ─────────────────────────────────────
    assert.ok(sawListReplying, "回合运行中会话列表灯未亮（症状 2 复现）");
  } finally {
    page.close();
  }
});

// 队列接力：回合 A 在跑时发 B（排队），A 的 done 到达后 B 起跑。
// 这是 G-BE 修复的**直接目标场景** —— A 的 done 迟到时，不得把 B 的 pending 清掉。
// （这也是 G-BE「迟到 done 不误抹新轮」在浏览器 + 真后端层的唯一证据。）
//
// 判据（服务端是「在不在跑」的真值源，UI 灯必须跟随它，无漏）：
//   · 服务端报告在跑时，UI 灯必须亮（允许 1 次采样延迟）；
//   · 接力期间至少观察到 2 次「在跑且灯亮」（A、B 各一次）。
test("E2E：队列接力时下一轮仍亮灯（迟到 done 不误清）", { skip: SKIP }, async (t) => {
  assert.ok(await isoUp(), `隔离实例未就绪：${ISO.base} —— 先 bash scripts/mindfs-iso.sh start`);
  const { uid } = isoAccount();

  const created = await runTurn({ content: "只回复两个字：收到" });
  const key = created.key;

  const page = await launchBrowser({ tag: "queue-ind" });
  try {
    await login(page);
    await openSession(page, key);
    assert.ok(await waitForInput(page), "输入框未出现");

    // 发 A，等它真的起跑（否则 B 不是「排队」而是「新回合」）。
    await sendFromComposer(page, "第一轮：只回复两个字：甲");
    assert.ok(await waitForServerReplying(uid, key), "第一轮未起跑");

    // A 在跑时发 B —— 应排队。
    await sendFromComposer(page, "第二轮：只回复两个字：乙");

    // 采样整个 A→B 接力期间：(服务端在跑?, UI 灯数)
    const samples = [];
    let emptySince = 0;
    const deadline = Date.now() + 120000;
    while (Date.now() < deadline) {
      const srvHas = (await serverReplying(uid)).includes(key);
      const ui = await listReplyingCount(page);
      samples.push({ srvHas, ui });
      if (srvHas) {
        emptySince = 0;
      } else if (emptySince === 0) {
        emptySince = Date.now();
      } else if (Date.now() - emptySince > 5000 && samples.filter((s) => s.srvHas).length >= 3) {
        break; // 已经稳定结束
      }
      await sleep(400);
    }

    // 断言 1：服务端在跑时，UI 灯不得连续灭超过 1 次采样。
    let missRun = 0;
    let maxMiss = 0;
    for (const s of samples) {
      if (s.srvHas && s.ui === 0) {
        missRun += 1;
        maxMiss = Math.max(maxMiss, missRun);
      } else {
        missRun = 0;
      }
    }
    assert.ok(
      maxMiss <= 1,
      `服务端在跑但 UI 灯灭（连续 ${maxMiss} 次采样）—— 症状 2 复现。样本：${JSON.stringify(samples)}`,
    );

    // 断言 2：B 真的跑过 —— 否则「接力」根本没发生，断言 1 只在验 A。
    const exs = await readExchanges(key, "proj");
    const allText = exs.map((e) => String(e.content || "")).join("\n");
    assert.ok(allText.includes("甲"), "第一轮（甲）的回复缺失");
    assert.ok(allText.includes("乙"), "第二轮（乙）的回复缺失 —— B 没跑，接力未发生");

    // 断言 3：全部结束后灯灭。
    await sleep(3000);
    assert.equal(await listReplyingCount(page), 0, "全部回合结束后会话列表仍显示「正在回复」");
  } finally {
    page.close();
  }
});

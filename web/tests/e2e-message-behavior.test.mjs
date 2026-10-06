// E2E：消息渲染行为（跑在**隔离实例**上，见 scripts/mindfs-iso.sh）。
//
// 为什么这一层不能省：仓库的其余测试都在单测层或源码契约层，而下面这几条钉的是
// **跨模块接线 + 真实时序 + 真后端回包**，三层单测都够不到。尤其第一条与第二条 ——
// 用 户报的那个 bug（完成瞬间正文消失、切走再切回才恢复）**只有这一层能给证据**。
//
// 触发：MINDFS_E2E=1 node --test tests/e2e-message-behavior.test.mjs
// 未设该变量时整文件跳过 —— 普通 `npm test` 不受影响（那一层已经有 18 条刻意的红）。
//
// 成本：一轮真实回合实测 $0.112。本文件**跑两轮**（一轮建会话、一轮被观察），约 $0.22/次。
// 长会话靠往 JSONL 追加合成行造（不花钱、且确定）。
import test from "node:test";
import assert from "node:assert/strict";
import {
  ISO,
  isoUp,
  runTurn,
  appendSyntheticRows,
  sendFromComposer,
  launchBrowser,
  login,
  openSession,
  dumpTimeline,
  dumpTimelineStable,
  countInJsonl,
  countRows,
  markerFor,
  readExchanges,
  sleep,
} from "./e2e/harness.mjs";

const ENABLED = process.env.MINDFS_E2E === "1";
const SKIP = ENABLED ? false : "设 MINDFS_E2E=1 才跑；需要隔离实例：bash scripts/mindfs-iso.sh start";

const SESSION_WINDOW_SIZE = 20; // 与 services/session.ts 的常量一致

/** 按可见文本点按钮（仓库无 playwright，这是最稳的 DOM 驱动方式）。 */
async function clickButtonByText(page, matcher) {
  return page.ev(`(() => {
    const hit = [...document.querySelectorAll('button')].find((b) => ${matcher});
    if (!hit) return 'not-found';
    hit.click();
    return 'clicked';
  })()`);
}

test("E2E：消息渲染行为（隔离实例）", { skip: SKIP }, async (t) => {
  assert.ok(await isoUp(), `隔离实例未就绪：${ISO.base} —— 先 bash scripts/mindfs-iso.sh start`);

  // ── 夹具：一轮真回合建出会话（空项目里 DOM 层建不出第一个会话）────────────────
  const created = await runTurn({ content: "只回复两个字：收到" });
  const key = created.key;
  // 不钉 LLM 的逐字输出（它会多说几句，实测回过「先读历史。…知道了」）——
  // 渲染测试只关心「真有一轮助手内容」，关键词用 includes 即可。
  assert.match(created.assistantText, /收到/, `第一轮没有得到助手的「收到」，帧：${created.frames.join(",")}`);

  // ── 造长会话：往 JSONL 追加合成行。窗口是 20，所以 32 行能让「窗口是子集」成立 ──
  const before = await readExchanges(key);
  const maxSeq = Math.max(...before.map((e) => Number(e.seq) || 0), 0);
  const lastSeq = appendSyntheticRows(key, 30, { startSeq: maxSeq, prefix: "合成行" });
  assert.equal((await readExchanges(key)).length, maxSeq + 30, "追加后库里行数不对");

  const page = await launchBrowser({ tag: "msg-e2e" });
  t.after(() => page.close());
  await login(page);
  await openSession(page, key);

  let baseline = await dumpTimelineStable(page);

  await t.test("冷启动：渲染窗口内的行，且末行是本轮助手行（协议半）", async () => {
    assert.ok(!baseline.err, `时间线读不到：${baseline.err}`);
    // 窗口 = 最新 20 条 → seq 从 lastSeq-19 到 lastSeq
    assert.equal(
      baseline.count,
      SESSION_WINDOW_SIZE,
      `冷启动应渲染窗口内 ${SESSION_WINDOW_SIZE} 行，实得 ${baseline.count}`,
    );
    assert.equal(
      String(baseline.items.at(-1).seq),
      String(lastSeq),
      "末行的 seq 应为会话最大 seq（最新的那条）",
    );
  });

  await t.test("完成瞬间：不得塌成只剩用户行，且必须发起窗口重锚定", async () => {
    const beforeDone = await dumpTimelineStable(page);
    const apiBefore = page.apiRequests.length;

    // 第二轮从**输入框**发（走 UI ⇒ 覆盖乐观回声那条路径），而不是走 WS —— 这样同一轮
    // 既观察了「完成瞬间」，又验证了「用户自己那条消息在完成前后都必须可见且不重复」。
    const uiText = "回复三个字：知道了";
    await sendFromComposer(page, uiText);
    await sleep(1500);

    // 回声：发送后立刻应看到自己那条（乐观行），且**只有一条**
    const justSent = await dumpTimeline(page);
    assert.equal(
      (justSent.items || []).filter((i) => String(i.len) > 0).length > 0,
      true,
      "发送后时间线不应为空",
    );

    // 等这一轮**落盘**（轮询 API，不用 CDP 的 WS 帧事件 —— 实测那组事件在这个环境里
    // 收不到任何帧，而 API 是确定性的）。落盘即 done 之后的事，时序足够紧。
    const agentRowsBefore = (await readExchanges(key)).filter(
      (e) => e.role === "agent" || e.role === "assistant",
    ).length;
    const doneDeadline = Date.now() + 180000;
    let turnLanded = false;
    while (Date.now() < doneDeadline) {
      const rows = await readExchanges(key).catch(() => []);
      const agents = rows.filter((e) => e.role === "agent" || e.role === "assistant").length;
      if (agents > agentRowsBefore) {
        turnLanded = true;
        break;
      }
      await sleep(700);
    }
    assert.ok(turnLanded, `UI 发送的那一轮未在 180s 内落盘（agent 行仍是 ${agentRowsBefore} 条）`);

    const afterDone = await dumpTimelineStable(page);

    // ① 机制级断言（比看项数强）：done 之后客户端**必须**对本会话发起窗口重锚定请求。
    //    线上真机复现里这条是 0 —— 那道门用了 isSessionStreaming，在监听者里恒为真，
    //    于是重锚定恒被跳过，而同一处理器更早已把瞬时尾巴清掉 ⇒ 正文消失。
    const reanchored = page.apiRequests
      .slice(apiBefore)
      .filter((r) => r.url.includes(`/api/sessions/${key}`) && r.url.includes("latest="));
    assert.ok(
      reanchored.length >= 1,
      `done 之后没有对本会话发起窗口重锚定请求（新增请求：${page.apiRequests
        .slice(apiBefore)
        .map((r) => r.url.replace(ISO.base, ""))
        .join(" · ") || "无"}）`,
    );

    // ② 观感断言：时间线不得塌成「只剩用户行」
    assert.ok(
      afterDone.count >= beforeDone.count,
      `完成瞬间时间线塌了：${beforeDone.count} 项 → ${afterDone.count} 项`,
    );
    assert.ok(
      afterDone.total >= beforeDone.total * 0.9,
      `完成瞬间正文长度明显下降：${beforeDone.total} → ${afterDone.total} 字`,
    );

    // ③ 乐观回声：完成后**恰好出现一次**（少一次=被清掉，多一次=与持久行重复）
    const echoCount = (await page.ev(`(() => {
      const content = document.querySelector('[data-mindfs-session-content-width]');
      const col = content && content.firstElementChild;
      if (!col) return 0;
      return [...col.children].filter((c) => (c.textContent || '').includes(${JSON.stringify(uiText)})).length;
    })()`));
    assert.equal(
      echoCount,
      1,
      `用户消息在时间线里应恰好出现 1 次（0 = 被清掉，>1 = 与持久行重复），实得 ${echoCount}`,
    );
  });

  await t.test("应用内切走再切回：项数与正文长度都不得下降", async () => {
    const beforeSwitch = await dumpTimelineStable(page);
    assert.ok(beforeSwitch.count > 0, "切走前时间线是空的，后续断言无意义");

    // 切到 Workspace 视图（SessionViewer 卸载），再点回该会话（重新挂载 + init 取窗）
    assert.equal(await clickButtonByText(page, "/^\\s*Workspace\\s*$/.test(b.textContent || '')"), "clicked");
    await sleep(2500);
    const away = await dumpTimeline(page);
    assert.ok(away.err || away.count === 0 || away.count > 0, "（离场态不参与断言）");

    const back = await clickButtonByText(page, "/只回复两个字/.test(b.textContent || '')");
    assert.equal(back, "clicked", "没能点回该会话（会话项按钮没找到）");

    // 先证明**真的回到了这个会话**，再比项数 —— 否则「没切回来」会被误读成「内容变少了」
    const deadline = Date.now() + 20000;
    let onSession = false;
    while (Date.now() < deadline) {
      const url = await page.ev("location.href");
      const dump = await dumpTimeline(page);
      if (String(url).includes(key) && !dump.err && dump.count > 0) {
        onSession = true;
        break;
      }
      await sleep(600);
    }
    assert.ok(onSession, `点回后没有回到会话 ${key}（URL=${await page.ev("location.href")}）`);

    const after = await dumpTimelineStable(page);
    // 线上复现时这里是 137 项 → 2 项。这条断言就是它的回归护栏。
    assert.ok(
      after.count >= beforeSwitch.count,
      `切走再切回后时间线变少了：${beforeSwitch.count} 项 → ${after.count} 项`,
    );
    assert.ok(
      after.total >= beforeSwitch.total * 0.9,
      `切走再切回后正文变少了：${beforeSwitch.total} 字 → ${after.total} 字`,
    );
  });

  await t.test(
    "跳转到较早消息后，尾部不得出现渲染空洞（冲突②）",
    {
      skip:
        "驱动不到：会话搜索开关只在「会话列表」布局渲染，而可驱动的 URL 状态" +
        "（?root=&session=&view=chat）下侧栏是文件树 —— 展开 .mindfs-sidebar-resize-rail 之后仍是文件树，" +
        "DOM 里既没有该开关也没有搜索输入框（实测两次）。改为在抽出的纯函数上做单元断言，" +
        "见 tests/session-overlay-unit.test.mjs 的「冲突②」用例：那里能确定性地构造出这个状态。",
    },
    async () => {
      // 保留本条的**判据原文**，等哪天有稳定入口就能直接接上：
      //   跳转前记下末行（最大 seq）；触发 targetSeq 跳转后，那一行必须仍然渲染。
      // 触发路径（读代码得到）：搜索命中 → useSessionSearch 设 session.search_seq →
      // App.tsx:8268 传成 targetSeq → SessionViewer:1876 的 `targetSeq>0 && !inRange` 分支
      // 用 setVisibleExchanges(winExchanges) 整体替换窗口、且不推进 latestSeq ⇒ A2 丢弃尾部。
      // 只在 ≤80 行（未虚拟化）时 DOM 里能看到全部行；超过阈值要靠滚动验证。
    },
  );

  await t.test("查库判据：区分「导入层写了两行」与「渲染层渲染两次」", async () => {
    // 判据工具本身也要有测试。线上真机复现时「同一段正文两遍」有两种成因，
    // 症状一样但层次不同：库里 2 行 → 写入/导入层重复；库里 1 行 → 渲染层把一行渲染了两次。
    //
    // **判据不得依赖 agent 的措辞** —— 实测它会回「读取失败：文件不存在 …」这类内容，
    // 关键词时有时无（第一版断言就是这么挂的）。所以：
    //   · 用**我完全控制**的合成行做计数断言；
    //   · 助手行只断言**条数**（每轮恰好一条 —— 重复写入会让它变多）。
    assert.equal(countRows(key, { includes: "合成行 #" }), 30, "合成行应为 30 行");
    assert.equal(
      countRows(key, { role: "user", includes: "合成行 #" }),
      15,
      "合成行按角色交替 ⇒ user 恰好 15 行（role 过滤是否生效）",
    );
    // 助手行分两部分：合成行里按角色交替的那 15 条 + 两轮真回合各 1 条 = 17。
    // 把两部分分开钉：多出来的只可能是「写入/导入层重复写」。
    assert.equal(
      countRows(key, { role: "agent", includes: "合成行 #" }),
      15,
      "合成助手行应为 15 条（30 行按角色交替）",
    );
    assert.equal(
      countRows(key, { role: "agent" }),
      17,
      "助手行总数应为 17（15 合成 + 2 真回合）—— 多了就是写入/导入层重复",
    );
    assert.equal(
      countRows(key, { role: "user", includes: "只回复两个字" }),
      1,
      "第一轮的用户消息应恰好一行",
    );
    assert.equal(
      countRows(key, { role: "user", includes: "回复三个字" }),
      1,
      "第二轮的用户消息应恰好一行",
    );
    // 判据工具的边界：只按内容数「合成行 #」会同时命中两种角色 ⇒ 这正是为什么要带 role
    assert.equal(
      countInJsonl(key, "合成行 #"),
      30,
      "只按内容计数的口径（30 = 两种角色之和，与 role 过滤后的 15 对照）",
    );
  });
});

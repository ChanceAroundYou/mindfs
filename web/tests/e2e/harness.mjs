// E2E 夹具：隔离实例的原语集合。**用例只写断言，不重写这些。**
//
// 为什么要有它（都是我实测踩出来的，别在下一个探针里重踩）：
//   · 登录要处理「输入框不是 textarea」—— 原生 value setter 会
//     `TypeError: Illegal invocation`，必须用 CDP 的 `Input.insertText` 打到聚焦元素；
//   · 打开会话必须带 `session=<key>&view=chat`，只给 `view=chat` 会被应用改写成
//     `view=workspace`，于是 `[data-onboarding=message-input]` 数量为 0；
//   · 建会话只能走 **WS 协议层**（`session.message` + `key=""`）—— 空项目页里根本没有
//     输入框，DOM 层建不出第一个会话；
//   · WS 握手必须带 `client_id`，缺了它服务端直接回 `client_id required`；
//   · 读会话的回包是**扁平**的，`exchanges` 在顶层、不在 `session` 下。
//
// 成本纪律：一轮真实回合实测 $0.112（input 22421 / output 2 tokens）。
// 所以本夹具**每轮 E2E 只跑一轮真回合**，之后靠 `appendSyntheticRows` 往 JSONL 追加
// 合成行来造长会话 —— 追加的行既便宜又确定，而窗口/overlay 那些行为只依赖行数与 seq。
import { spawn } from "node:child_process";
import { readFileSync, appendFileSync, existsSync } from "node:fs";
import path from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

export const ISO = {
  root: process.env.MINDFS_ISO_ROOT || "/tmp/mindfs-iso",
  port: process.env.MINDFS_ISO_PORT || "7431",
  chrome: process.env.MINDFS_CHROME || "/home/xiaokubao/.local/bin/chromium",
};
ISO.base = `http://127.0.0.1:${ISO.port}/mindfs`;
ISO.proj = path.join(ISO.root, "proj");

/** 读隔离账户（user / pass / uid）。uid 必须带上：项目与会话**按账户分**。 */
export function isoAccount() {
  const f = path.join(ISO.root, "account");
  if (!existsSync(f)) throw new Error(`隔离账户不存在：${f}（先跑 scripts/mindfs-iso.sh start）`);
  const [user, pass, uid] = readFileSync(f, "utf8").trim().split("\n");
  if (!user || !pass || !uid) throw new Error("隔离账户文件格式不对（应为 user/pass/uid 三行）");
  return { user, pass, uid };
}

export async function isoUp(timeoutMs = 3000) {
  try {
    const r = await fetch(`${ISO.base}/health`, { signal: AbortSignal.timeout(timeoutMs) });
    return r.ok;
  } catch {
    return false;
  }
}

/** 某会话的落盘文件 —— 这是「导入层 vs 渲染层」的判据工具。 */
export function jsonlFor(key) {
  return path.join(ISO.proj, ".mindfs", "sessions", `${key}.jsonl`);
}

/** 数某段正文在**库里**出现几次。出现两次 ⇒ 导入/写入层写了两行；一次 ⇒ 渲染层渲染了两次。 */
export function countInJsonl(key, needle) {
  const f = jsonlFor(key);
  if (!existsSync(f)) return 0;
  return readFileSync(f, "utf8")
    .split("\n")
    .filter(Boolean)
    .filter((line) => {
      try {
        return String(JSON.parse(line).content || "").includes(needle);
      } catch {
        return false;
      }
    }).length;
}

/**
 * 按**角色**精确计数落盘行 —— 「查库判据」的正确形态。
 *
 * 只用 `countInJsonl` 挑串很容易自伤：回复「知道了」而用户消息是「回复三个字：知道了」时，
 * 同一根串在两行里都命中，得到 2 —— 看起来像「库里写了两行」，其实是判据选错了。
 * 所以判「导入层有没有写重复」必须 **role + 内容** 一起限定。
 */
export function countRows(key, { role, includes } = {}) {
  const f = jsonlFor(key);
  if (!existsSync(f)) return 0;
  return readFileSync(f, "utf8")
    .split("\n")
    .filter(Boolean)
    .filter((line) => {
      try {
        const o = JSON.parse(line);
        if (role && String(o.role || "") !== role) return false;
        if (includes && !String(o.content || "").includes(includes)) return false;
        return true;
      } catch {
        return false;
      }
    }).length;
}

/** 读会话（注意：回包是**扁平**的）。 */
export async function readExchanges(key, rootId = "proj") {
  const { uid } = isoAccount();
  const r = await fetch(`${ISO.base}/api/sessions/${key}?root=${rootId}&user=${uid}`);
  if (!r.ok) throw new Error(`读会话失败 HTTP ${r.status}`);
  const body = await r.json();
  return Array.isArray(body.exchanges) ? body.exchanges : [];
}

/** 该会话的合成行标记 —— 搜索时必须唯一，否则跨会话会命中别的会话（实测踩过）。 */
export function markerFor(key) {
  return `合成行-${String(key).slice(-6)}`;
}

/** 往隔离项目的会话 JSONL 追加合成行 —— 造长会话用，不花钱。 */
export function appendSyntheticRows(key, count, { startSeq = 0, prefix = "" } = {}) {
  const tag = prefix || markerFor(key);
  const rows = [];
  let seq = startSeq;
  for (let i = 0; i < count; i += 1) {
    seq += 1;
    const role = i % 2 === 0 ? "user" : "agent";
    rows.push(
      JSON.stringify({
        seq,
        role,
        source: "live",
        agent: "claude",
        model: "default",
        content: `${tag} #${seq}：${"内容".repeat(6)}`,
        timestamp: new Date(Date.UTC(2026, 9, 6, 10, 0, seq % 60)).toISOString(),
      }),
    );
  }
  appendFileSync(jsonlFor(key), rows.join("\n") + "\n");
  return seq;
}

/**
 * 跑**一轮真实回合**（WS 协议层）。key 为空则新建会话。
 * 返回 { key, frames, assistantText }。
 */
export async function runTurn({ content, rootId = "proj", key = "", timeoutMs = 150000 } = {}) {
  const { uid } = isoAccount();
  const clientId = `e2e-${Date.now()}`;
  const ws = new WebSocket(
    `ws://127.0.0.1:${ISO.port}/mindfs/ws?client_id=${clientId}&user=${uid}`,
  );
  const frames = [];
  const seen = new Map();
  await new Promise((res, rej) => {
    ws.onopen = res;
    ws.onerror = () => rej(new Error("WS 握手失败（记得带 client_id）"));
    setTimeout(() => rej(new Error("WS 连接超时")), 10000);
  });
  ws.onmessage = (m) => {
    try {
      const o = JSON.parse(m.data);
      frames.push(o.type);
      if (o.type === "session.user_message" && o.payload?.session_key) {
        seen.set("key", o.payload.session_key);
      }
      if (o.type === "session.done") seen.set("done", true);
    } catch {
      /* 非 JSON 帧忽略 */
    }
  };
  ws.send(
    JSON.stringify({
      id: `e2e-${Date.now()}`,
      type: "session.message",
      payload: {
        root_id: rootId,
        session_key: key || undefined,
        content,
        type: "chat",
        agent: "claude",
        model: "default",
        agent_mode: "",
        effort: "",
        fast_service: "",
        shell: "",
      },
    }),
  );
  const deadline = Date.now() + timeoutMs;
  while (!seen.get("done") && Date.now() < deadline) await sleep(500);
  ws.close();
  const sessionKey = key || seen.get("key");
  if (!sessionKey) throw new Error(`没拿到 session_key（帧：${frames.join(",")}）`);
  if (!seen.get("done")) throw new Error(`未在 ${timeoutMs}ms 内收到 session.done`);
  const exs = await readExchanges(sessionKey, rootId);
  const assistant = exs.filter((e) => e.role === "agent" || e.role === "assistant").pop();
  return { key: sessionKey, frames, assistantText: String(assistant?.content || "") };
}

/** 极简 CDP 客户端（仓库没有 playwright，这是既有先例用的办法）。 */
export async function launchBrowser({ tag = "e2e", port = 9379 + (process.pid % 100) } = {}) {
  const proc = spawn(
    ISO.chrome,
    [
      "--headless=new",
      `--remote-debugging-port=${port}`,
      "--no-sandbox",
      "--disable-gpu",
      "--disable-dev-shm-usage",
      `--user-data-dir=${path.join("/tmp", `cdp-${tag}`)}`,
      "about:blank",
    ],
    { stdio: "ignore" },
  );
  let target = null;
  for (let i = 0; i < 150; i += 1) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      target = list.find((t) => t.type === "page");
      if (target) break;
    } catch {
      /* 还没起来 */
    }
    await sleep(100);
  }
  if (!target) {
    proc.kill();
    throw new Error("chromium 未能就绪");
  }
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((res, rej) => {
    ws.onopen = res;
    ws.onerror = rej;
  });
  let id = 0;
  const pend = new Map();
  const framesRx = [];
  const framesTx = [];
  const consoleErrors = [];
  const apiRequests = [];
  ws.onmessage = (m) => {
    const g = JSON.parse(m.data);
    if (g.id && pend.has(g.id)) {
      const p = pend.get(g.id);
      pend.delete(g.id);
      g.error ? p.rej(new Error(JSON.stringify(g.error))) : p.res(g.result);
      return;
    }
    const pl = g.params?.payloadData || "";
    const t = (pl.match(/"type":"([a-z_.]+)"/) || [])[1];
    if (g.method === "Network.webSocketFrameReceived" && t) framesRx.push(t);
    if (g.method === "Network.webSocketFrameSent" && t) framesTx.push(t);
    if (g.method === "Network.responseReceived" && g.params.response.url.includes("/api/")) {
      apiRequests.push({ status: g.params.response.status, url: g.params.response.url });
    }
    if (g.method === "Runtime.consoleAPICalled" && g.params.type === "error") {
      consoleErrors.push((g.params.args || []).map((a) => a.value ?? a.description ?? "").join(" ").slice(0, 200));
    }
  };
  const send = (method, params = {}) =>
    new Promise((res, rej) => {
      const i = ++id;
      pend.set(i, { res, rej });
      ws.send(JSON.stringify({ id: i, method, params }));
    });
  const ev = (expression) =>
    send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }).then((r) =>
      r.exceptionDetails ? `ERR ${String(r.exceptionDetails.exception?.description || "").slice(0, 200)}` : r.result.value,
    );

  await send("Page.enable");
  await send("Runtime.enable");
  await send("Network.enable");
  await send("Emulation.setDeviceMetricsOverride", { width: 1500, height: 1000, deviceScaleFactor: 1, mobile: false });

  return {
    send,
    ev,
    framesRx,
    framesTx,
    consoleErrors,
    apiRequests,
    navigate: async (url, waitMs = 6000) => {
      await send("Page.navigate", { url });
      await sleep(waitMs);
    },
    close: () => {
      try {
        ws.close();
      } catch {
        /* ignore */
      }
      proc.kill();
    },
  };
}

/** 登录（用 CDP 的 Input.insertText —— 原生 setter 会 TypeError）。 */
export async function login(page) {
  const { user, pass } = isoAccount();
  const base = `${ISO.base}/`;
  await page.navigate(base, 7000);
  if (!(await page.ev(`document.querySelectorAll('input[type=password]').length`))) {
    return { ok: true, already: true };
  }
  await page.ev(`(() => {
    const set = (el, v) => { const s = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set; s.call(el, v); el.dispatchEvent(new Event('input',{bubbles:true})); };
    set(document.querySelector('input[autocomplete=username]') || document.querySelector('input[type=text]'), ${JSON.stringify(user)});
    set(document.querySelector('input[type=password]'), ${JSON.stringify(pass)});
    return 1;
  })()`);
  await page.ev(`(() => { const b = [...document.querySelectorAll('button')].find(x => /sign\\s*in|登录/i.test(x.textContent||'')); if (b) b.click(); return 1; })()`);
  await sleep(7000);
  const stuck = await page.ev(`document.querySelectorAll('input[type=password]').length`);
  if (stuck) throw new Error("登录失败（仍在登录页）");
  return { ok: true, already: false };
}

/** 打开某个会话（必须带 session=，否则 view=chat 会被改写成 workspace）。 */
export async function openSession(page, key, rootId = "proj", waitMs = 8000) {
  await page.navigate(`${ISO.base}/?root=${rootId}&session=${encodeURIComponent(key)}&view=chat`, waitMs);
}

/**
 * 从输入框发一条消息（**走 UI**，因此覆盖乐观回声那条路径）。
 *
 * 两个实测坑：`data-onboarding=message-input` 是包裹元素而不是 textarea（用原生 value setter
 * 会 `TypeError: Illegal invocation`）；写入必须用 CDP 的 `Input.insertText` 打到**聚焦元素**上。
 */
export async function sendFromComposer(page, text) {
  const focused = await page.ev(`(() => {
    const el = document.querySelector('[data-onboarding=message-input]');
    if (!el) return 'no-input';
    const t = el.matches('textarea,input,[contenteditable=true]')
      ? el : (el.querySelector('textarea,input,[contenteditable=true]') || el);
    t.focus();
    return 'focused';
  })()`);
  if (focused !== "focused") throw new Error(`输入框不可用：${focused}`);
  await page.send("Input.insertText", { text });
  await sleep(300);
  const clicked = await page.ev(`(() => {
    const b = document.querySelector('[data-onboarding=send-action]');
    if (!b) return 'no-send';
    b.click();
    return 'clicked';
  })()`);
  if (clicked !== "clicked") throw new Error(`发送键不可用：${clicked}`);
  return true;
}

/** 时间线快照：项数 / 正文总长 / 每项的 seq 与 user-message-index。 */
export async function dumpTimeline(page) {
  const raw = await page.ev(`(() => {
    const content = document.querySelector('[data-mindfs-session-content-width]');
    if (!content) return JSON.stringify({ err: 'no-content-marker' });
    const col = content.firstElementChild;
    if (!col) return JSON.stringify({ err: 'no-column' });
    const items = [...col.children].map((c) => {
      const t = (c.textContent || '').replace(/\\s+/g, ' ').trim();
      return { seq: c.getAttribute('data-session-seq'), umi: c.getAttribute('data-user-message-index'), len: t.length };
    }).filter((x) => x.len > 0);
    return JSON.stringify({ count: items.length, total: items.reduce((a, b) => a + b.len, 0), items });
  })()`);
  try {
    return JSON.parse(raw);
  } catch {
    return { err: `parse-fail:${String(raw).slice(0, 120)}` };
  }
}

/**
 * 稳定快照：连抓两次一致才返回。
 *
 * 为什么要它：切会话/重锚定是异步的（窗口回包 → 合并 → 重渲染），固定 sleep 之后抓到的
 * 可能是**渲染中途**的一帧 —— 那会得到「项数变少了」的假阳性。断言不该因此放松，
 * 该修的是测量：等它收敛。
 */
export async function dumpTimelineStable(page, { timeoutMs = 20000, intervalMs = 600 } = {}) {
  const deadline = Date.now() + timeoutMs;
  let prev = await dumpTimeline(page);
  while (Date.now() < deadline) {
    await sleep(intervalMs);
    const next = await dumpTimeline(page);
    const same =
      !prev.err &&
      !next.err &&
      prev.count === next.count &&
      prev.total === next.total &&
      JSON.stringify(prev.items?.map((i) => `${i.seq}/${i.len}`)) ===
        JSON.stringify(next.items?.map((i) => `${i.seq}/${i.len}`));
    if (same) return next;
    prev = next;
  }
  return prev;
}

export { sleep };

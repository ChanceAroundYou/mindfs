// E2E 观测：驱动 home(local)↔pc 节点切换，采集 [session-list]/[managed-roots]/[node-switch] 日志
// 并在切换窗口内按帧采样右面板（MultiProjectSessionList）分组头颜色，检测"全蓝"帧。
// 运行：MIND_FS_E2E=1 node web/tests/session-list-observe.e2e.mjs
// 输出：全部日志与采样结果写入 /tmp/mindfs-e2e-session-list.json，摘要打印到 stdout。
import fs from "node:fs";
import { createRequire } from "node:module";

let chromium;
{
  const require = createRequire(import.meta.url);
  const candidates = [
    "playwright",
    "/home/xiaokubao/.local/share/hermes-web-ui/node_modules/playwright",
    "/home/xiaokubao/.npm/_npx/e41f203b7505f1fb/node_modules/playwright",
    "/home/xiaokubao/.local/share/usage-checker/node_modules/playwright",
  ];
  for (const p of candidates) {
    try {
      ({ chromium } = require(p));
      break;
    } catch {}
  }
}
if (!chromium) {
  console.log("session-list-observe skipped: playwright not installed");
  process.exit(0);
}
if (!process.env.MIND_FS_E2E) {
  console.log("session-list-observe skipped: set MIND_FS_E2E=1 to run");
  process.exit(0);
}

const APP = process.env.MIND_FS_APP_URL || "http://127.0.0.1:7331/mindfs/";
const ROUNDS = Number(process.env.MIND_FS_ROUNDS || 4);
const BLUE = "rgb(59, 130, 246)"; // #3b82f6 PALETTE[0]
const ts = () => new Date().toISOString();

const browser = await chromium.launch({ headless: true, args: ["--no-sandbox", "--disable-setuid-sandbox"] });
const context = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await context.newPage();

const logs = [];
const sampleLog = []; // {t, phase, groups:[{label,color}]}
page.on("console", (m) => {
  const t = m.text();
  if (/\[session-list\]|\[managed-roots\]|\[node-switch\]/.test(t)) {
    logs.push(`${ts()} [${m.type()}] ${t}`);
  }
});
page.on("pageerror", (e) => logs.push(`${ts()} [pageerror] ${e.message}`));
page.on("requestfailed", (r) => {
  const u = r.url();
  if (!/\/api\//.test(u)) return;
  logs.push(`${ts()} [requestfailed] ${u} :: ${String(r.failure()?.errorText || "")}`);
});

// 本机即 home：把 local 节点 url 重写为页面同源（127.0.0.1:7331），
// 否则 local 的 API 调用走 home.xiaokubao.space 公网 hairpin 在无头浏览器内失败。
await page.route("**/api/nodes", async (route) => {
  try {
    const resp = await route.fetch();
    let body = await resp.json();
    if (Array.isArray(body)) {
      body = body.map((n) =>
        String(n.id) === "local" ? { ...n, url: APP.replace(/\/+$/, "") } : n,
      );
    }
    await route.fulfill({ response: resp, json: body, headers: { ...resp.headers(), "content-type": "application/json" } });
  } catch {
    await route.continue();
  }
});

await page.addInitScript(() => {
  try {
    localStorage.setItem(
      "mindfs-onboarding-state",
      JSON.stringify({ version: 2, completedAt: "2026-08-20T00:00:00.000Z" }),
    );
  } catch {}
});

// 页面内工具：左侧树「节点分组 X 下的项目按钮」打标记后由主线程点击
async function clickProjectInNodeGroup(nodeLabel, projectName) {
  const found = await page.evaluate(
    ({ nodeLabel, projectName }) => {
      document
        .querySelectorAll("[data-mindfs-click-target]")
        .forEach((el) => el.removeAttribute("data-mindfs-click-target"));
      const headers = [...document.querySelectorAll("div")].filter((d) => {
        const st = d.getAttribute("style") || "";
        if (!/height:\s*22px/.test(st)) return false;
        if (d.querySelector("button")) return false; // 左侧树头为 span 变体（无按钮）
        const span = [...d.querySelectorAll("span")].find((s) =>
          (s.getAttribute("style") || "").includes("font-weight: 600"),
        );
        return !!span && span.textContent === nodeLabel;
      });
      for (const h of headers) {
        const group = h.parentElement;
        if (!group) continue;
        for (const b of group.querySelectorAll("li button")) {
          if ((b.innerText || "").trim() === projectName) {
            b.setAttribute("data-mindfs-click-target", "1");
            return true;
          }
        }
      }
      return false;
    },
    { nodeLabel, projectName },
  );
  if (!found) return false;
  await page.locator('[data-mindfs-click-target="1"]').first().click();
  return true;
}

// 采样右面板分组头：高度22px 且含 button+svg polyline（MultiProjectSessionList 分组头）
let lastSampleError = null;
async function sampleRightPanelHeaders() {
  try {
    return await page.evaluate(() => {
      const out = [];
      for (const d of document.querySelectorAll("div")) {
        const st = d.getAttribute("style") || "";
        if (!/height:\s*22px/.test(st)) continue;
        const btn = d.querySelector("button");
        if (!btn || !btn.querySelector("svg polyline")) continue;
        const span = [...d.querySelectorAll("span")].find((s) =>
          (s.getAttribute("style") || "").includes("font-weight: 600"),
        );
        if (!span) continue;
        out.push({ label: span.textContent, color: getComputedStyle(span).color });
      }
      return out;
    });
  } catch (err) {
    lastSampleError = String(err);
    return null; // 上下文销毁等瞬时错误：跳过该帧
  }
}

async function sampleWindow(phase, ms = 900, cadence = 30) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    const groups = await sampleRightPanelHeaders();
    if (groups) sampleLog.push({ t: ts(), phase, groups });
    await page.waitForTimeout(cadence);
  }
}

// 模拟 WS 事件触发的多项目重拉（refreshManagedRoots → loadMultiProjectSessionGroups）
async function dispatchNodesChanged() {
  await page.evaluate(() => {
    try {
      window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
    } catch {}
  });
}

function summarizeBlueFrames() {
  // any-blue：任一分组蓝（local 本色即蓝，仅参考）；all-blue：全部分组皆蓝（真·全蓝帧）
  const anyBlue = sampleLog.filter((f) => f.groups.some((g) => g.color === BLUE));
  const allBlue = sampleLog.filter((f) => f.groups.length >= 2 && f.groups.every((g) => g.color === BLUE));
  const byPhase = {};
  for (const f of allBlue) byPhase[f.phase] = (byPhase[f.phase] || 0) + 1;
  return { blueFrames: anyBlue.length, allBlueFrames: allBlue.length, allBlueByPhase: byPhase };
}

let targets = null;
let targetLocal = null;
let targetPc = null;
let clickFailures = [];
try {
  await page.goto(APP, { waitUntil: "domcontentloaded", timeout: 30_000 });
  const bootDeadline = Date.now() + 30_000;
  while (!logs.some((l) => l.includes("[session-list] boot")) && Date.now() < bootDeadline) {
    await page.waitForTimeout(250);
  }

  const treeHeaders = async () =>
    page.evaluate(() => {
      const names = [];
      for (const d of document.querySelectorAll("div")) {
        const st = d.getAttribute("style") || "";
        if (!/height:\s*22px/.test(st)) continue;
        if (d.querySelector("button")) continue;
        const span = [...d.querySelectorAll("span")].find((s) =>
          (s.getAttribute("style") || "").includes("font-weight: 600"),
        );
        if (span) names.push(span.textContent);
      }
      return names;
    });

  const treeDeadline = Date.now() + 30_000;
  let treeReady = false;
  while (Date.now() < treeDeadline) {
    const names = await treeHeaders();
    if (names.includes("local") && names.includes("pc")) {
      treeReady = true;
      break;
    }
    await page.waitForTimeout(300);
  }
  if (!treeReady) {
    throw new Error("tree node groups not ready (local+pc)");
  }

  const treeProjects = await page.evaluate(() => {
    const byNode = {};
    for (const d of document.querySelectorAll("div")) {
      const st = d.getAttribute("style") || "";
      if (!/height:\s*22px/.test(st)) continue;
      if (d.querySelector("button")) continue;
      const span = [...d.querySelectorAll("span")].find((s) =>
        (s.getAttribute("style") || "").includes("font-weight: 600"),
      );
      if (!span) continue;
      const nodeName = span.textContent;
      const group = d.parentElement;
      const names = [...(group?.querySelectorAll("li button") || [])].map((b) =>
        (b.innerText || "").trim(),
      );
      byNode[nodeName] = names;
    }
    return byNode;
  });
  const common = (treeProjects.local || []).filter((n) => (treeProjects.pc || []).includes(n));
  const requested = (process.env.MIND_FS_PROJECTS || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  // 本项目集：优先共同项目；否则逐节点取第一个项目（switching 目的在切节点，无需同名）
  targetLocal = requested.find((n) => (treeProjects.local || []).includes(n)) || common[0] || (treeProjects.local || [])[0] || null;
  targetPc = requested.find((n) => (treeProjects.pc || []).includes(n)) || common[0] || (treeProjects.pc || [])[0] || null;
  if (!targetLocal || !targetPc) {
    throw new Error(`no project per node: ${JSON.stringify(treeProjects)}`);
  }
  console.log(`targets: local="${targetLocal}" pc="${targetPc}" (common=${JSON.stringify(common)})`);

  // 预热：切一次（落缓存），再 reload，让后续每次刷新的 cache-apply 有缓存可用
  const warmupNode = treeProjects.local && treeProjects.local.length ? "local" : "pc";
  const warmupTarget = warmupNode === "local" ? targetLocal : targetPc;
  const firstOk = await clickProjectInNodeGroup(warmupNode, warmupTarget);
  if (!firstOk) clickFailures.push({ phase: "warmup", node: warmupNode, project: warmupTarget });
  await page.waitForTimeout(2500);
  await page.reload({ waitUntil: "domcontentloaded" });
  const boot2 = Date.now() + 30_000;
  while (!logs.some((l) => l.includes("[session-list] boot")) && Date.now() < boot2) {
    await page.waitForTimeout(250);
  }
  // 等缓存帧结束后的网络帧落地
  await page.waitForTimeout(2500);
  const cacheApplyCount = logs.filter((l) => l.includes("groups cache-apply")).length;
  console.log(`cache-apply seen so far: ${cacheApplyCount}`);

  // 切换循环：点击 → 立即 dispatch（模拟 WS 事件触发重拉）→ 密集采样
  for (let round = 1; round <= ROUNDS; round++) {
    for (const node of ["local", "pc"]) {
      const phase = `${round}:${node}`;
      const project = node === "local" ? targetLocal : targetPc;
      const ok = await clickProjectInNodeGroup(node, project);
      if (!ok) clickFailures.push({ round, node, project });
      await dispatchNodesChanged();
      await sampleWindow(phase, 900, 30);
      await page.waitForTimeout(150);
    }
  }
} catch (err) {
  logs.push(`${ts()} [script-error] ${err && err.stack ? err.stack : String(err)}`);
} finally {
  const summary = {
    app: APP,
    rounds: ROUNDS,
    targets: { local: targetLocal, pc: targetPc },
    clickFailures,
    lastSampleError,
    bootLine: logs.find((l) => l.includes("[session-list] boot")) || null,
    ...summarizeBlueFrames(),
    logs,
    samples: sampleLog,
  };
  fs.writeFileSync("/tmp/mindfs-e2e-session-list.json", JSON.stringify(summary, null, 2));
  console.log(`=== SUMMARY ===`);
  console.log(`boot: ${summary.bootLine || "(MISSING - 请确认新 bundle 已部署/无缓存)"}`);
  console.log(`clickFailures: ${JSON.stringify(summary.clickFailures)}`);
  console.log(`lastSampleError: ${summary.lastSampleError || "(none)"}`);
  console.log(`anyBlueFrames: ${summary.blueFrames}  allBlueFrames: ${summary.allBlueFrames}  allBlueByPhase: ${JSON.stringify(summary.allBlueByPhase)}`);
  console.log(`total log lines: ${logs.length}`);
  const blueByIdx = sampleLog
    .map((f, i) => ({ i, ...f }))
    .filter((f) => f.groups.some((g) => g.color === BLUE));
  console.log(`first 5 blue samples: ${JSON.stringify(blueByIdx.slice(0, 5), null, 1)}`);
  console.log(`full output: /tmp/mindfs-e2e-session-list.json`);
  await context.close();
  await browser.close();
}

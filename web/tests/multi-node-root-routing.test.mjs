import assert from "node:assert/strict";
import { chromium } from "playwright";

const pcURL = "https://pc.xiaokubao.space/mindfs/";
const homeOrigin = "https://home.xiaokubao.space";
const homeURL = `${homeOrigin}/mindfs`;

const browser = await chromium.launch({
  headless: true,
  args: ["--no-sandbox", "--disable-setuid-sandbox"],
});
const context = await browser.newContext({ ignoreHTTPSErrors: true });
const page = await context.newPage();
const requests = [];
page.on("request", (request) => {
  const url = request.url();
  if (/\/api\/(?:dirs|tree|git\/history|tasks)(?:\?|$)/.test(url)) requests.push(url);
});

await page.addInitScript(({ pcURL, homeURL }) => {
  localStorage.setItem("mindfs-onboarding-state", JSON.stringify({ version: 2, completedAt: "2026-08-20T00:00:00.000Z" }));
  localStorage.setItem("mindfs-multi-project-sessions", "true");
  localStorage.setItem("mindfs_active_node_id", "pc");
  localStorage.setItem("mindfs_nodes", JSON.stringify([
    { id: "local", name: "local", url: pcURL.replace(/\/$/, ""), color: "#6d5bcf" },
    { id: "home", name: "home", url: homeURL, color: "#e07a2f" },
  ]));
}, { pcURL, homeURL });

try {
  await page.goto(`${pcURL}?root=go`, { waitUntil: "networkidle", timeout: 30_000 });
  await page.waitForTimeout(1_000);
  const rootRequests = requests.filter((url) => /[?&]root=go(?:&|$)/.test(url));
  assert.ok(rootRequests.length > 0, "expected the home root to issue at least one root-bearing request");
  assert.ok(
    rootRequests.every((url) => url.startsWith(homeOrigin)),
    `root=go must only target home; got ${rootRequests.join(", ")}`,
  );
} finally {
  await context.close();
  await browser.close();
}

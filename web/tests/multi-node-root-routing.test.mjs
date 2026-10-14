import assert from "node:assert/strict";
import { test } from "vitest";

let chromium;
try {
  ({ chromium } = await import("playwright"));
} catch {
  chromium = undefined;
}

const enabled = process.env.MIND_FS_E2E === "1" && Boolean(chromium);

test.skipIf(!enabled)("multi-node root routing targets the node that owns the root", async () => {
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

  try {
    await page.addInitScript(({ pcURL: targetPcURL, homeURL: targetHomeURL }) => {
      localStorage.setItem("mindfs-onboarding-state", JSON.stringify({ version: 2, completedAt: "2026-08-20T00:00:00.000Z" }));
      localStorage.setItem("mindfs-multi-project-sessions", "true");
      localStorage.setItem("mindfs_active_node_id", "pc");
      localStorage.setItem("mindfs_nodes", JSON.stringify([
        { id: "local", name: "local", url: targetPcURL.replace(/\/$/, ""), color: "#6d5bcf" },
        { id: "home", name: "home", url: targetHomeURL, color: "#e07a2f" },
      ]));
    }, { pcURL, homeURL });

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
});

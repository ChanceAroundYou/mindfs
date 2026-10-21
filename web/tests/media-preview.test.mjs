import assert from "node:assert/strict";
import { test } from "vitest";
import { classifyVideo, isHtmlPreview } from "../src/services/mediaPreview.ts";
import { buildRawAssetURL } from "../src/services/file/rawAsset.ts";
import { accountSegmentFor } from "../src/services/net/base.ts";
import { applyNodesFromServer, setActiveNodeId } from "../src/services/net/nodeRegistry.ts";

// 预览类型判定 + 路径式原始资源 URL 的构造规则。
//
// 账户段必须在**路径**里：网页里的相对 URL（style.css、img/x.png）在浏览器里
// 解析时会丢掉查询串，账户在路径里才保得住「按账户分区」。

function stubBrowser({ origin, user }) {
  const store = new Map();
  if (user) store.set("mindfs.current_user", JSON.stringify(user));
  globalThis.window = {
    location: { origin, href: `${origin}/mindfs/` },
    localStorage: {
      getItem: (key) => (store.has(key) ? store.get(key) : null),
      setItem: (key, value) => store.set(key, String(value)),
      removeItem: (key) => store.delete(key),
    },
  };
}

const USER = { id: "u_local_1", username: "xiaokubao", role: "admin" };

test("media-preview", () => {
// --- 预览类型判定 ---
assert.equal(isHtmlPreview(".html"), true);
assert.equal(isHtmlPreview(".HTM"), true);
assert.equal(isHtmlPreview("", "text/html"), true);
assert.equal(isHtmlPreview(".txt"), false);
assert.equal(isHtmlPreview(".md"), false);

assert.equal(classifyVideo(".mp4"), "playable");
assert.equal(classifyVideo(".webm"), "playable");
assert.equal(classifyVideo(".mov"), "playable");
assert.equal(classifyVideo(".avi", "video/x-msvideo"), "unsupported");
assert.equal(classifyVideo(".mkv", "video/x-matroska"), "unsupported");
assert.equal(classifyVideo(".txt"), null);
assert.equal(classifyVideo(".png", "image/png"), null);

// --- 账户段：同源用 id，跨机器用用户名 ---
stubBrowser({ origin: "https://home.example", user: USER });

// 同源 → 用账户 id
assert.equal(accountSegmentFor("https://home.example/mindfs/api/file/raw/x/root/a.html"), "u_local_1");
// 跨机器 → 用用户名（账户 id 各机独立，带不过去）
assert.equal(accountSegmentFor("https://pc.example/mindfs/api/file/raw/x/root/a.html"), "xiaokubao");

// --- URL 构造 ---
{
  const url = buildRawAssetURL({ rootId: "myroot", path: "site/index.html" });
  assert.equal(url, "https://home.example/mindfs/api/file/raw/u_local_1/myroot/site/index.html");
}

// 路径段逐段编码：文件名带空格/中文不能把 URL 拆坏
{
  const url = buildRawAssetURL({ rootId: "my root", path: "docs/我的 页面.html" });
  assert.equal(
    url,
    "https://home.example/mindfs/api/file/raw/u_local_1/my%20root/docs/%E6%88%91%E7%9A%84%20%E9%A1%B5%E9%9D%A2.html",
  );
}

// 跨节点 → 账户段换成用户名，地址也换成那台节点的
{
  // 节点表里放一台别的机器并选中它：请求会打到它的地址，账户 id 带不过去。
  applyNodesFromServer([{ id: "pc", name: "pc", url: "https://pc.example/mindfs", color: "#fff" }]);
  setActiveNodeId("pc");
  const url = buildRawAssetURL({ rootId: "myroot", path: "index.html" });
  assert.equal(url, "https://pc.example/mindfs/api/file/raw/xiaokubao/myroot/index.html");
  setActiveNodeId(null);
  applyNodesFromServer([]);
}

// 没有登录账户 → 返回空串（调用方提示「预览不出来，请下载」），不拼半截 URL
{
  stubBrowser({ origin: "https://home.example", user: null });
  assert.equal(buildRawAssetURL({ rootId: "myroot", path: "index.html" }), "");
}

console.log("media-preview: ok");
});

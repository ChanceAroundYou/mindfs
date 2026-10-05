// 过期标签页自愈：只对带内容哈希的构建产物自动重载，且同一 URL 只重载一次。
// 纯 node/assert，直接 import TS 源码（node 22 类型剥离）。
import assert from "node:assert";
import fs from "node:fs";
import test from "node:test";
import {
  isVersionedAssetPath,
  shouldReloadForStaleAsset,
} from "../src/services/staleAssetRecovery.ts";

const RELOAD_KEY = "mindfs:stale-asset-reload";
const OLD_CHUNK = "/mindfs/assets/mermaid.core-CNLciIW1.js";
const NEW_CHUNK = "/mindfs/assets/mermaid.core-DeKIx6kp.js";

function memoryStorage() {
  const map = new Map();
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => {
      map.set(key, String(value));
    },
  };
}

test("只有带哈希的构建产物才算版本错配", () => {
  assert.ok(isVersionedAssetPath(OLD_CHUNK), "mermaid 懒加载包应命中");
  assert.ok(isVersionedAssetPath("/assets/index-9O1ZEnhO.js"), "根部署下也应命中");
  assert.ok(isVersionedAssetPath("/mindfs/assets/index-BzpwC3EJ.css"), "CSS 也应命中");
  // 名字固定、不带哈希：重载修不好，交给横幅提示
  assert.ok(!isVersionedAssetPath("/mindfs/pwa-192.png"));
  assert.ok(!isVersionedAssetPath("/mindfs/manifest.webmanifest"));
  assert.ok(!isVersionedAssetPath("/mindfs/assets/agents/claude.svg"), "assets 子目录不是构建产物");
  assert.ok(!isVersionedAssetPath("/mindfs/api/files/foo.js"), "后端路径不是构建产物");
  assert.ok(!isVersionedAssetPath(""));
});

test("同一路径只重载一次，换新哈希允许再试", () => {
  const storage = memoryStorage();
  assert.strictEqual(shouldReloadForStaleAsset(OLD_CHUNK, storage), true, "首次应重载");
  assert.strictEqual(
    shouldReloadForStaleAsset(OLD_CHUNK, storage),
    false,
    "同一 URL 再失败说明服务端真没这文件，不得死循环",
  );
  assert.strictEqual(shouldReloadForStaleAsset(NEW_CHUNK, storage), true, "新构建新哈希应再试");
  assert.strictEqual(storage.getItem(RELOAD_KEY), NEW_CHUNK, "记录最后重载过的路径");
});

test("非构建产物不重载，也不写标记", () => {
  const storage = memoryStorage();
  assert.strictEqual(shouldReloadForStaleAsset("/mindfs/pwa-192.png", storage), false);
  assert.strictEqual(storage.getItem(RELOAD_KEY), null);
});

test("storage 不可用时退回横幅提示（不重载）", () => {
  const denied = {
    getItem() {
      throw new Error("denied");
    },
    setItem() {
      throw new Error("denied");
    },
  };
  assert.strictEqual(shouldReloadForStaleAsset(OLD_CHUNK, denied), false);
});

test("main.tsx 两条资源失败路径都接到自愈上", () => {
  const mainSrc = fs.readFileSync(new URL("../src/main.tsx", import.meta.url), "utf8");
  const wired = mainSrc.match(/!recoverFromStaleAsset\(resourceURL\)/g) || [];
  assert.strictEqual(wired.length, 2, "window.error 与 unhandledrejection 都应先尝试自愈再弹横幅");
  assert.ok(
    /isEditingText\(\)/.test(mainSrc),
    "正在输入时应跳过重载，避免丢草稿",
  );
});

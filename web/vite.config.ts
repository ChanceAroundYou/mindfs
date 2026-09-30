import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/**
 * 单一规范化：与 web/src/services/prefix.ts 的 normalizePrefix 保持同一规则。
 * 从 VITE_MIND_FS_BASE 得到“无尾斜杠、以 / 开头或空字符串”的规范化部署前缀。
 * 空字符串 / "/" / "" → ""（根部署）；"mindfs" / "/mindfs/" → "/mindfs"。
 */
function normalizeBase(raw: string): string {
  const v = String(raw || "").trim().replace(/\/+$/, "");
  if (!v || v === "/") return "";
  return v.startsWith("/") ? v : `/${v}`;
}

function escapeRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * 每次构建生成唯一构建戳，注入所有 JS chunk + SW version。
 * 目的：Vite 内容哈希在源码未变时产物文件名不变 → SW 缓存名不变 →
 * service-worker.js 字节不变 → 浏览器永不重装 SW → SHELL_CACHE 永远返回旧 bundle。
 * 注入随机 stamp 后：chunk 字节必变 → 内容哈希必变 → 文件名必变 → SW 缓存名必变 →
 * SW 字节必变 → 浏览器重装 SW → activate 清理旧缓存，缓存死锁被打破。
 */
function buildStamp(): string {
  return process.env.MFS_BUILD_STAMP || crypto.randomBytes(8).toString("hex");
}

function listPublicAssets(publicDir: string): string[] {
  if (!fs.existsSync(publicDir)) {
    return [];
  }

  const urls: string[] = [];

  const walk = (currentDir: string) => {
    for (const entry of fs.readdirSync(currentDir, { withFileTypes: true })) {
      const absolutePath = path.join(currentDir, entry.name);
      if (entry.isDirectory()) {
        walk(absolutePath);
        continue;
      }

      const relativePath = path.relative(publicDir, absolutePath).split(path.sep).join("/");
      if (!relativePath || relativePath === "service-worker.js") {
        continue;
      }
      urls.push(`./${relativePath}`);
    }
  };

  walk(publicDir);
  return urls.sort();
}

type BundleItem = {
  fileName: string;
  type: string;
};

function listShellBundleAssets(bundle: Record<string, BundleItem>): string[] {
  return Object.values(bundle)
    .filter((item) => item.fileName !== "service-worker.js")
    .filter((item) => !item.fileName.endsWith(".map"))
    .filter((item) => item.fileName.startsWith("assets/index-"))
    .map((item) => `./${item.fileName}`)
    .sort();
}

function buildServiceWorker(precacheUrls: string[], version: string, relayAliasPrefix: string): string {
  return `const SHELL_CACHE = "mindfs-shell-${version}";
const RELAY_ALIAS = ${JSON.stringify(relayAliasPrefix)};
const RUNTIME_CACHE = "mindfs-runtime-${version}";
const OFFLINE_URL = new URL("./offline.html", self.location.href).toString();
const INDEX_URL = new URL("./index.html", self.location.href).toString();
const PRECACHE_URLS = ${JSON.stringify(precacheUrls, null, 2)};

// Strip the deploy sub-path scope prefix (e.g. /mindfs/) so API detection
// works when MindFS is served under a sub-path behind nginx.
function scopeRelativePathname(pathname) {
  const scopeBase = new URL(self.registration.scope).pathname.replace(/\\/$/, "");
  if (scopeBase && pathname.startsWith(scopeBase + "/")) {
    return pathname.slice(scopeBase.length) || "/";
  }
  if (scopeBase && pathname === scopeBase) {
    return "/";
  }
  return pathname;
}

function normalizedPathname(pathname) {
  const relayPrefixMatch = pathname.match(/^\\/n\\/[^/]+(?=\\/|$)/);
  if (!relayPrefixMatch) {
    return pathname;
  }
  const normalized = pathname.slice(relayPrefixMatch[0].length);
  return normalized || "/";
}

self.addEventListener("install", (event) => {
  event.waitUntil((async () => {
    const cache = await caches.open(SHELL_CACHE);
    await cache.addAll(PRECACHE_URLS.map((url) => new URL(url, self.location.href).toString()));
    await self.skipWaiting();
  })());
});

self.addEventListener("activate", (event) => {
  event.waitUntil((async () => {
    const cacheKeys = await caches.keys();
    await Promise.all(cacheKeys
      .filter((key) => key !== SHELL_CACHE && key !== RUNTIME_CACHE)
      .map((key) => caches.delete(key)));
    await self.clients.claim();
  })());
});

self.addEventListener("push", (event) => {
  event.waitUntil((async () => {
    const payload = readPushPayload(event);
    const title = payload.title || "MindFS";
    const options = {
      body: payload.body || "",
      tag: payload.tag || undefined,
      renotify: Boolean(payload.renotify),
      requireInteraction: Boolean(payload.requireInteraction),
      icon: payload.icon ? new URL(payload.icon, self.location.href).toString() : new URL("./pwa-192.png", self.location.href).toString(),
      badge: payload.badge ? new URL(payload.badge, self.location.href).toString() : new URL("./pwa-192.png", self.location.href).toString(),
      data: {
        ...(payload.data || {}),
        url: payload.url || "./",
      },
    };
    await self.registration.showNotification(title, options);
  })());
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil((async () => {
    const targetURL = new URL(event.notification.data?.url || "./", self.location.href).toString();
    const clientList = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const client of clientList) {
      if (new URL(client.url).origin === self.location.origin) {
        await client.focus();
        if ("navigate" in client) {
          await client.navigate(targetURL);
        }
        return;
      }
    }
    await self.clients.openWindow(targetURL);
  })());
});

function readPushPayload(event) {
  if (!event.data) {
    return {};
  }
  try {
    return event.data.json() || {};
  } catch {
    return { title: "MindFS", body: event.data.text() };
  }
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") {
    return;
  }

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) {
    return;
  }
  const pathname = scopeRelativePathname(normalizedPathname(url.pathname));
  if (pathname.startsWith(RELAY_ALIAS)) {
    return;
  }
  if (pathname.startsWith("/api/") || pathname === "/api" || pathname === "/ws" || pathname === "/health") {
    return;
  }

  if (request.mode === "navigate") {
    event.respondWith(handleNavigationRequest(request));
    return;
  }

  event.respondWith(handleStaticRequest(request));
});

async function handleNavigationRequest(request) {
  const cache = await caches.open(SHELL_CACHE);
  try {
    // 不再把 index.html 写回缓存：它是整条缓存链的根，一旦缓存住，
    // 旧标签页就会一直拿旧壳子去请求早已不存在的旧 chunk（内容哈希对不上，
    // 表现为 DevTools 里加载了一整批过期 js）。离线兜底仍读 precache 里那份。
    return await fetch(request);
  } catch {
    const cachedIndex = await cache.match(INDEX_URL);
    if (cachedIndex) {
      return cachedIndex;
    }
    const offlineResponse = await cache.match(OFFLINE_URL);
    if (offlineResponse) {
      return offlineResponse;
    }
    throw new Error("offline");
  }
}

async function handleStaticRequest(request) {
  const shellCache = await caches.open(SHELL_CACHE);
  const cachedShellResponse = await shellCache.match(request);
  if (cachedShellResponse) {
    return cachedShellResponse;
  }

  const runtimeCache = await caches.open(RUNTIME_CACHE);
  const cachedRuntimeResponse = await runtimeCache.match(request);
  if (cachedRuntimeResponse) {
    // stale-while-revalidate：先拿缓存顶上（离线也能开），同时后台拉新版本。
    // 纯 cache-first 会把旧 chunk 一直喂给老标签页，直到 RUNTIME_CACHE 改名
    // 加上一次手动刷新才收敛。
    revalidate(request, runtimeCache);
    return cachedRuntimeResponse;
  }

  try {
    const response = await fetch(request);
    if (response.ok) {
      runtimeCache.put(request, response.clone()).catch(() => {});
    }
    return response;
  } catch {
    if (request.destination === "image") {
      const iconResponse = await shellCache.match(new URL("./pwa-192.png", self.location.href).toString());
      if (iconResponse) {
        return iconResponse;
      }
    }
    return new Response("", {
      status: 504,
      statusText: "Asset Unavailable",
    });
  }
}

// 后台刷新缓存，不阻塞本次响应。失败就算了：离线时保持旧副本总比没有强。
function revalidate(request, cache) {
  fetch(request)
    .then((response) => {
      if (response.ok) {
        return cache.put(request, response.clone());
      }
      return undefined;
    })
    .catch(() => {});
}
`;
}

function appShellHTMLPlugin(opts: {
  deployPrefix: string;
  assetRoot: string;
  relayAliasPrefix: string;
}) {
  const { deployPrefix, assetRoot, relayAliasPrefix } = opts;
  // 构建期按同一规范化部署前缀生成主包匹配正则：覆盖 /<prefix>/assets/index-* 与
  // relay 别名 /<prefix>-assets/index-*，避免硬编码 /mindfs-assets。
  // 注意：正则字面量内的 "/" 必须转义为 "\/"，否则注入后成为非法 flags（SyntaxError）。
  const assetRootNoSlash = assetRoot.replace(/\/+$/, "");
  const relayNoSlash = relayAliasPrefix.replace(/\/+$/, "");
  const escLiteral = (value: string) => escapeRegex(value).replace(/\//g, "\\/");
  const mainAssetReSource = `^\\/(?:${escLiteral(relayNoSlash)}|${escLiteral(assetRootNoSlash)}\\/assets)\\/index-[^/]+\\.(?:js|css)$`;
  return {
    name: "mindfs-app-shell-html",
    transformIndexHtml(html: string) {
      const pwaLinks = [
        `    <link rel="manifest" href="${deployPrefix}/manifest.webmanifest" />`,
        `    <link rel="apple-touch-icon" href="${deployPrefix}/apple-touch-icon.png" />`,
      ].join("\n");
      const pwaMeta = [
        '    <meta name="apple-mobile-web-app-capable" content="yes" />',
        '    <meta name="apple-mobile-web-app-status-bar-style" content="default" />',
        '    <meta name="apple-mobile-web-app-title" content="MindFS" />',
        '    <meta name="mobile-web-app-capable" content="yes" />',
      ].join("\n");
      const appShell = process.env.VITE_APP_SHELL === "1";
      return html
        .replace("<!--MINDFS_FAVICON_HREF-->", `${deployPrefix}/favicon.svg`)
        .replace("/<__MINDFS_MAIN_ASSET_RE__>/", `/${mainAssetReSource}/i`)
        .replace("<!--APP_SHELL_PWA_LINKS-->", appShell ? "" : pwaLinks)
        .replace("<!--APP_SHELL_PWA_META-->", appShell ? "" : pwaMeta);
    },
  };
}

const PWA_ONLY_ASSETS = new Set([
  "manifest.webmanifest",
  "apple-touch-icon.png",
  "pwa-192.png",
  "pwa-512.png",
  "pwa-icon-maskable.svg",
  "pwa-icon.svg",
  "pwa-maskable-192.png",
  "pwa-maskable-512.png",
  "service-worker.js",
]);

function appShellExcludeAssetsPlugin() {
  let resolvedOutDir = "";
  return {
    name: "mindfs-app-shell-exclude-assets",
    apply: "build" as const,
    configResolved(config: { build: { outDir: string } }) {
      resolvedOutDir = config.build.outDir;
    },
    closeBundle() {
      if (process.env.VITE_APP_SHELL !== "1" || !resolvedOutDir) {
        return;
      }
      // public/ 目录文件由 Vite 直接复制，不经过 bundle，需在 closeBundle 里删除
      for (const fileName of PWA_ONLY_ASSETS) {
        const filePath = path.join(resolvedOutDir, fileName);
        if (fs.existsSync(filePath)) {
          fs.rmSync(filePath);
        }
      }
    },
  };
}

function autoPrecachePlugin(relayAliasPrefix: string) {
  return {
    name: "mindfs-auto-precache",
    apply: "build" as const,
    generateBundle(this: { emitFile: (file: { type: "asset"; fileName: string; source: string }) => void }, _options: unknown, bundle: Record<string, BundleItem>) {
      if (process.env.VITE_APP_SHELL === "1") {
        return;
      }
      const publicDir = path.resolve(__dirname, "public");
      const publicAssets = listPublicAssets(publicDir);
      const shellBundleAssets = listShellBundleAssets(bundle);
      const precacheUrls = Array.from(
        new Set(["./", "./index.html", ...publicAssets, ...shellBundleAssets]),
      );
      const version = crypto
        .createHash("sha256")
        .update(JSON.stringify(precacheUrls))
        .digest("hex")
        .slice(0, 12);

      this.emitFile({
        type: "asset",
        fileName: "service-worker.js",
        source: buildServiceWorker(precacheUrls, version, relayAliasPrefix),
      });
    },
  };
}

/**
 * 构建戳插件：给每个 JS chunk 注入随机构建戳。
 * 改写 chunk 字节 → Vite 内容哈希必变 → 产物文件名必变 → precacheUrls 必变
 * → SW version 必变 → service-worker.js 字节必变 → 浏览器重装 SW 清旧缓存。
 */
function buildStampPlugin() {
  const stamp = buildStamp();
  return {
    name: "mindfs-build-stamp",
    apply: "build" as const,
    renderChunk(code: string, chunk: { fileName: string }) {
      if (!chunk.fileName.endsWith(".js")) {
        return null;
      }
      const banner = `globalThis.__MFS_BUILD_STAMP__="${stamp}";`;
      return { code: banner + code, map: null };
    },
  };
}

export default defineConfig(({ mode }) => {
  // 单一真源：从 VITE_MIND_FS_BASE 得到的规范化部署前缀驱动 base / HTML 注入 / SW / 代理。
  const env = loadEnv(mode, process.cwd(), "");
  const rawBase = process.env.VITE_MIND_FS_BASE ?? env.VITE_MIND_FS_BASE ?? "/mindfs";
  const deployPrefix = normalizeBase(rawBase); // 无尾斜杠、以 / 开头或空字符串
  const viteBase = deployPrefix === "" ? "/" : `${deployPrefix}/`;
  const assetRoot = deployPrefix === "" ? "/" : `${deployPrefix}/`;
  const relayAliasPrefix = deployPrefix === "" ? "/assets/" : `${deployPrefix}-assets/`;

  // 代理键也来自同一部署前缀：/mindfs/api、/mindfs/ws，根部署退化为 /api、/ws。
  const proxy: Record<string, unknown> = {};
  if (deployPrefix) {
    proxy[`${deployPrefix}/api`] = {
      target: "http://localhost:7331",
      changeOrigin: true,
      rewrite: (p: string) => p.replace(new RegExp(`^${escapeRegex(deployPrefix)}`), ""),
    };
    proxy[`${deployPrefix}/ws`] = {
      target: "ws://localhost:7331",
      ws: true,
      rewriteWsOrigin: true,
    };
  } else {
    proxy["/api"] = "http://localhost:7331";
    proxy["/ws"] = { target: "ws://localhost:7331", ws: true };
  }

  return {
    base: viteBase,
    plugins: [
      tailwindcss(),
      react(),
      appShellHTMLPlugin({ deployPrefix, assetRoot, relayAliasPrefix }),
      appShellExcludeAssetsPlugin(),
      autoPrecachePlugin(relayAliasPrefix),
      buildStampPlugin(),
    ],
    server: {
      host: "0.0.0.0",
      proxy,
    },
  };
});

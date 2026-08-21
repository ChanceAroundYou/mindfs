import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

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

function buildServiceWorker(precacheUrls: string[], version: string): string {
  return `const SHELL_CACHE = "mindfs-shell-${version}";
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
  if (pathname.startsWith("/mindfs-assets/")) {
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
    const response = await fetch(request);
    cache.put(INDEX_URL, response.clone()).catch(() => {});
    return response;
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
`;
}

function appShellHTMLPlugin() {
  return {
    name: "mindfs-app-shell-html",
    transformIndexHtml(html: string) {
      const pwaLinks = [
        '    <link rel="manifest" href="/manifest.webmanifest" />',
        '    <link rel="apple-touch-icon" href="/apple-touch-icon.png" />',
      ].join("\n");
      const pwaMeta = [
        '    <meta name="apple-mobile-web-app-capable" content="yes" />',
        '    <meta name="apple-mobile-web-app-status-bar-style" content="default" />',
        '    <meta name="apple-mobile-web-app-title" content="MindFS" />',
        '    <meta name="mobile-web-app-capable" content="yes" />',
      ].join("\n");
      const appShell = process.env.VITE_APP_SHELL === "1";
      return html
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

function autoPrecachePlugin() {
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
        source: buildServiceWorker(precacheUrls, version),
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

export default defineConfig({
  base: "./",
  plugins: [tailwindcss(), react(), appShellHTMLPlugin(), appShellExcludeAssetsPlugin(), autoPrecachePlugin(), buildStampPlugin()],
  server: {
    host: "0.0.0.0",
    proxy: {
      "/api": "http://localhost:7331",
      "/ws": {
        target: "ws://localhost:7331",
        ws: true,
      },
    },
  },
});

/**
 * 过期标签页自愈。
 *
 * 哈希资源是按需（动态 import）取的，所以「部署前打开的标签页」完全可能在本批 chunk
 * 已被换掉之后才去请求旧哈希 —— 就是 main.tsx 那条「前端资源缺失或无法加载」横幅的来源。
 * 因为 index.html 是 no-cache、哈希 chunk 是 immutable，重新加载一次就能拿到新壳子；
 * 这里只判断「值不值得自动重载一次」。
 */

const RELOAD_KEY = "mindfs:stale-asset-reload";

// 只有构建产物带内容哈希（assets/<name>-<hash>.js|css）。
// pwa 图标、manifest 这些名字固定，重载也修不好，交给横幅提示。
export function isVersionedAssetPath(path: string): boolean {
  return /(?:^|\/)assets\/[^/]+\.(?:js|css)$/i.test(path);
}

/**
 * 同一路径只重载一次：重载后若同一 URL 仍失败，说明服务端确实没有这个文件，
 * 再重载就是死循环。换 URL（新构建、新哈希）则允许再试。
 */
export function shouldReloadForStaleAsset(
  path: string,
  storage: Pick<Storage, "getItem" | "setItem">,
): boolean {
  if (!isVersionedAssetPath(path)) {
    return false;
  }
  try {
    if (storage.getItem(RELOAD_KEY) === path) {
      return false;
    }
    storage.setItem(RELOAD_KEY, path);
    return true;
  } catch {
    // 隐私模式等 storage 不可用：退回横幅提示，不重载
    return false;
  }
}

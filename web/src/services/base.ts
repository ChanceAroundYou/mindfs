import { getApiBaseURL, getWsBaseURL, isBrowserRuntime } from "./runtime";
import { DEPLOY_PREFIX, withDeployPrefix } from "./prefix";
import { currentUser } from "./authGate";

function ensureLeadingSlash(path: string): string {
  return path.startsWith("/") ? path : `/${path}`;
}

function joinURL(baseURL: string, path: string): string {
  return `${baseURL.replace(/\/+$/, "")}${ensureLeadingSlash(path)}`;
}

/**
 * 把参数并进已有 URL 的查询串（不会产出 `?a=1?b=2`）。
 * 导出给少数需要自己拼查询串的调用方复用。
 */
export function appendQuery(url: string, params?: URLSearchParams | string): string {
  const extra = typeof params === "string" ? params.replace(/^\?/, "") : String(params || "");
  if (!extra) {
    return url;
  }
  const hashIndex = url.indexOf("#");
  const hash = hashIndex >= 0 ? url.slice(hashIndex) : "";
  const base = hashIndex >= 0 ? url.slice(0, hashIndex) : url;
  const queryIndex = base.indexOf("?");
  const path = queryIndex >= 0 ? base.slice(0, queryIndex) : base;
  const existing = queryIndex >= 0 ? base.slice(queryIndex + 1) : "";
  return `${path}?${existing ? `${existing}&` : ""}${extra}${hash}`;
}

/**
 * 目标 URL 是否就是发这个页面的服务器（逐字同源，ws/wss 归一后比 host）。
 *
 * **不要**在这里放宽成「本机节点表里的地址也算本机」：那张表里的远端条目
 * 恰恰指向**别的机器**（实测 PC 端节点表里就有对方那台）。按表放宽会把本机账户 id
 * 发给没有该账户的机器，对方 404、项目全空。
 */
export function isSameServerAsPage(url: string): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    const target = new URL(url, window.location.href);
    const page = new URL(window.location.href);
    const normalize = (protocol: string) =>
      protocol === "ws:" ? "http:" : protocol === "wss:" ? "https:" : protocol;
    return (
      normalize(target.protocol) === normalize(page.protocol) &&
      target.host === page.host
    );
  } catch {
    return false;
  }
}

/**
 * 给请求打到「当前登录账户」的分区上。
 *
 * 服务端按这个参数分区存储、不做鉴权（见 docs/multi-user-prd.md §1.1），
 * 所以它等价于「我这次请求算哪个账户的」。缺省时服务端回落到主账户。
 *
 * **同源**（本机）：带账户 id，最精确。
 *
 * **跨机器**：id 不能带——账户表每台机器独立，id 是各自随机生成的
 * （实测本机 u_pc_admin、另一台 u_2d_hoAd3ZMEimSSH），带过去必然 404。
 * 但**用户名**是同一个人的稳定标识（两边都叫 xiaokubao），所以跨机器改带
 * `user=<用户名>`，由对方按用户名解析到**它本地**的那个账户。
 *
 * 不带的话对方会回落到**它自己的主账户**，把别人机器上的项目当成你的返回回来——
 * 不报错、不提示（这是实测踩过的坑，比 404 危险得多）。
 *
 * 对方没有这个用户名时服务端回 404 unknown_user，由调用方按节点处理（见 api.ts）。
 */
function withAccountUser(url: string): string {
  if (!url) {
    return url;
  }
  const user = currentUser();
  if (!user?.id) {
    return url;
  }
  const queryIndex = url.indexOf("?");
  const query = queryIndex >= 0 ? url.slice(queryIndex + 1) : "";
  if (new URLSearchParams(query).has("user")) {
    return url;
  }
  if (isSameServerAsPage(url)) {
    return appendQuery(url, `user=${encodeURIComponent(user.id)}`);
  }
  // 跨机器：id 带不过去，改带用户名
  const name = String(user.username || "").trim();
  if (!name) {
    return url;
  }
  return appendQuery(url, `user=${encodeURIComponent(name)}`);
}

/** 不带账户参数的基础路径；appURL/wsURL 复用它，避免 user= 被塞到参数前面。 */
function basePath(path: string, nodeId?: string): string {
  const pathname = ensureLeadingSlash(path);
  const apiBaseURL = getApiBaseURL(nodeId);
  if (apiBaseURL) {
    return joinURL(apiBaseURL, pathname);
  }
  // under /mindfs even when no base URL (e.g. native shell fallback or relative)
  if (DEPLOY_PREFIX) return withDeployPrefix(pathname);
  return pathname;
}

export function appPath(path: string, nodeId?: string): string {
  return withAccountUser(basePath(path, nodeId));
}

export function appURL(path: string, params?: URLSearchParams, nodeId?: string): string {
  return withAccountUser(appendQuery(basePath(path, nodeId), params));
}

export function wsURL(path: string, params?: URLSearchParams, nodeId?: string): string {
  const wsBaseURL = getWsBaseURL(nodeId);
  const pathname = ensureLeadingSlash(path);
  let target = wsBaseURL ? joinURL(wsBaseURL, pathname) : (DEPLOY_PREFIX ? withDeployPrefix(pathname) : pathname);
  if (!wsBaseURL && isBrowserRuntime()) {
    const { protocol, origin } = window.location;
    if (protocol === "https:") {
      target = joinURL(`wss://${origin.slice("https://".length)}`, pathname);
    } else if (protocol === "http:") {
      target = joinURL(`ws://${origin.slice("http://".length)}`, pathname);
    }
  }
  return withAccountUser(appendQuery(target, params));
}

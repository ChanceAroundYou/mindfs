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
 * 给请求打到「当前登录账户」的分区上。
 *
 * 服务端按这个参数分区存储、不做鉴权（见 docs/multi-user-prd.md §1.1），
 * 所以它等价于「我这次请求算哪个账户的」。缺省时服务端回落到主账户。
 *
 * 只给**同源**请求带账户 id：账户表是每台机器独立的，把本机账户 id 塞给另一个节点
 * 会让那边查不到而 404。跨节点请求由那台机器自己决定用哪个账户（回落到它的主账户），
 * 想在别的节点上用别的账户，就直连那台机器的页面登录。
 */
function withAccountUser(url: string): string {
  if (!url) {
    return url;
  }
  const user = currentUser();
  if (!user?.id) {
    return url;
  }
  if (typeof window !== "undefined") {
    try {
      const target = new URL(url, window.location.href);
      if (target.origin !== window.location.origin) {
        return url;
      }
    } catch {
      return url;
    }
  }
  const queryIndex = url.indexOf("?");
  const query = queryIndex >= 0 ? url.slice(queryIndex + 1) : "";
  if (new URLSearchParams(query).has("user")) {
    return url;
  }
  return appendQuery(url, `user=${encodeURIComponent(user.id)}`);
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

import { getApiBaseURL, getWsBaseURL, isBrowserRuntime } from "../platform/runtime";
import { DEPLOY_PREFIX, withDeployPrefix } from "./prefix";
import { deriveLocalNodeBase } from "./nodeBase";
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

/**
 * 路径式端点用的账户段。
 *
 * 与 withAccountUser 同一条规则：同源用账户 id，跨机器用用户名
 * （账户表每台机器独立，id 带不过去；用户名是跨机器唯一能对齐的东西）。
 *
 * 与查询串式的差别只有一处：这里拿不到 id 时退回用户名（服务端 id 和用户名都认），
 * 而 withAccountUser 是干脆不带账户。路径式没有账户段就取不到文件，
 * 退回用户名比掉回主账户准。
 *
 * 空串表示没有账户 —— 调用方要自己处理（多数是「预览不出来，请下载」）。
 */
export function accountSegmentFor(url: string): string {
  const user = currentUser();
  if (!user) {
    return "";
  }
  if (isSameServerAsPage(url)) {
    const id = String(user.id || "").trim();
    if (id) {
      return id;
    }
  }
  return String(user.username || "").trim();
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

/**
 * 路径式端点的基础路径（**不带**账户查询串 —— 账户在路径段里）。
 *
 * 与 appPath 的区别只有这一个：appPath 会注入 user=，而路径式端点必须把账户
 * 放进路径（浏览器解析相对 URL 时会丢查询串），再带一份 user= 就是两处真相。
 */
export function appBasePath(nodeId?: string): string {
  const apiBaseURL = getApiBaseURL(nodeId);
  if (apiBaseURL) {
    return apiBaseURL.replace(/\/+$/, "");
  }
  return DEPLOY_PREFIX.replace(/\/+$/, "");
}

/**
 * 打「发这个页面的服务器」的路径——不跟随当前选中的节点。
 *
 * appPath 会跟随当前节点（getApiBaseURL(undefined) 取 active node），这对文件/会话是对的：
 * 你要看哪个节点的数据就打哪个节点。但**账户管理**不行：账户表每台机器一份，
 * 而登录走的是页面服务器（authGate.authBaseURL）。用 appPath 时，「登录到 home、
 * 当前节点是 pc」会让面板列出 pc 的账户，在 home 建的账户看起来就消失了。
 * 身份在哪台机器，账户管理就在哪台机器。
 */
export function pageServerPath(path: string): string {
  if (typeof window === "undefined") {
    return withAccountUser(ensureLeadingSlash(path));
  }
  const base = deriveLocalNodeBase(window.location.origin, DEPLOY_PREFIX);
  return withAccountUser(joinURL(base, ensureLeadingSlash(path)));
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

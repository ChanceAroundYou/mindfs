import { bootstrapService } from "./bootstrap";
import { e2eeService } from "./e2ee";
import { logout } from "./authGate";
import { isSameServerAsPage } from "./base";

export class APIError extends Error {
  status: number;
  payload: any;
  constructor(status: number, payload: any, fallback: string) {
    super(String(payload?.message || payload?.error || fallback));
    this.name = "APIError";
    this.status = status;
    this.payload = payload;
  }
}

// 兼容旧名：保留导出，避免一次性改动过大遗漏处崩溃
export const ProtectedAPIError = APIError;

let accountResetInFlight = false;

/**
 * 这条请求打的是不是**发这个页面的那台服务器**。
 *
 * 必需，因为 `unknown_user` 有两种含义，处理方式相反：
 *   ① 本机账户被删/被转移 —— 该登出，否则整页一直 404；
 *   ② 另一台机器上没有这个账户（账户表每台机器独立）—— **绝不能登出**，
 *      那只是那台节点不该给你看东西，本机账户还好好的。
 * 旧代码把两者都当成 ①，于是访问一个没有本账户的节点会直接把用户踢下线。
 */
function targetsPageServer(input: RequestInfo | URL): boolean {
  try {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    return isSameServerAsPage(raw);
  } catch {
    return false;
  }
}

/**
 * 服务端说「这个账户不存在」。
 *
 * 只有**当前服务器**说这话才代表账户真没了（见 targetsPageServer）。
 * 跨机器的那份不是错误，是「该节点没有你的账户」——交给调用方按节点展示。
 */
function handleAccountGone(status: number, payload: any, input: RequestInfo | URL): void {
  if (status !== 404 || accountResetInFlight) {
    return;
  }
  const code = String(payload?.error || "");
  if (!code.startsWith("unknown_user")) {
    return;
  }
  // 别的节点没有这个账户：不动登录态，也不重载页面
  if (!targetsPageServer(input)) {
    return;
  }
  accountResetInFlight = true;
  logout();
  if (typeof window !== "undefined") {
    window.location.reload();
  }
}

export async function fetchJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T> {
  const response = await fetch(input, init);
  const payload = await response.json().catch(() => ({} as any));
  if (!response.ok) {
    handleAccountGone(response.status, payload, input);
    throw new APIError(response.status, payload, `request failed: ${response.status}`);
  }
  return payload as T;
}

export async function fetchMaybeJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T | null> {
  const response = await fetch(input, init);
  if (response.status === 204 || response.status === 304) return null as T;
  const payload = await response.json().catch(() => ({} as any));
  if (!response.ok) {
    handleAccountGone(response.status, payload, input);
    throw new APIError(response.status, payload, `request failed: ${response.status}`);
  }
  return payload as T;
}

export function protectedAPIReady(): boolean {
  return bootstrapService.canUseProtectedAPI();
}

export async function protectedFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  if (!protectedAPIReady()) {
    throw new Error("api_not_ready");
  }
  return e2eeService.protectedFetch(input, init);
}

export async function protectedJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T> {
  if (!protectedAPIReady()) {
    throw new Error("api_not_ready");
  }
  const response = await e2eeService.protectedFetch(input, init);
  const payload = await e2eeService.parseProtectedJSONResponse<any>(response).catch(() => ({} as any));
  if (!response.ok) {
    // 和其它两个 helper 一致：本机账户被删时也要登出，否则整页卡在 404。
    // （这条以前漏了，而 /api/dirs 正是走它。）
    handleAccountGone(response.status, payload, input);
    throw new APIError(response.status, payload, `request failed: ${response.status}`);
  }
  return payload as T;
}

const NODE_RETRY_DELAYS_MS = [400, 1200];

// 每个节点按域名直连，域名可能同时挂着多个 A 记录（实测：其中一个网卡离线后地址仍被广播，
// 浏览器撞上去要等好几秒才回退）。单次失败会让该节点的项目/会话整块缺席，而重拉只在
// WS 重连或用户操作时才发生——实测能长时间不恢复。这里对同一节点补几次重试，
// 让浏览器重新建连、重新挑地址；服务端明确回 4xx 时不重试。
export async function withNodeRetry<T>(run: () => Promise<T>): Promise<T> {
  for (let attempt = 0; ; attempt++) {
    try {
      return await run();
    } catch (err) {
      const retryable =
        err instanceof APIError
          ? err.status >= 500
          : String((err as Error)?.message || "") !== "api_not_ready";
      if (!retryable || attempt >= NODE_RETRY_DELAYS_MS.length) throw err;
      await new Promise((resolve) => setTimeout(resolve, NODE_RETRY_DELAYS_MS[attempt]));
    }
  }
}

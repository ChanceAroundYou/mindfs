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

/**
 * 跨节点请求的挂起兜底（毫秒）。
 *
 * 为什么必须有：远端节点挂掉时**不一定**快速失败。实测 pc 断联后
 * `https://pc.xiaokubao.space/...` 25 秒仍无响应（`http_code=000`，连接建立/被黑洞丢弃后
 * 服务端永不回话），而 DNS 解析失败只要 0.1 秒。浏览器 `fetch` 对「连上了但不回话」这种
 * 情况**没有内置超时**（约 2 分钟后才由 TCP 层报错），于是：
 *
 *   - `Promise.all` 扇出的会话列表/工作台重拉被这一个节点拖住，
 *     其余节点的数据回来了也提交不上去（`setLoading(false)` 永远不执行）；
 *   - `withNodeRetry` 每次重试又各挂 2 分钟，越拖越久。
 *
 * 有了这个 deadline，挂起会在 10 秒内变成一次**确定的失败**，调用方按既有分支处理
 * （会话列表保留该节点旧分组 + 弹「节点加载失败」；工作台跳过该节点），本机数据照常刷新。
 *
 * 取 10 秒：本机节点的 `/api/sessions?multi_root=1` 实测 100–400ms，10 秒留了
 * 一个数量级的余量；再长只会让「真挂住」的感觉更久。
 */
export const NODE_REQUEST_TIMEOUT_MS = 10000;

/** 超过这个毫秒数的请求视为「跨节点重拉」才加 deadline：本机请求不给人为上限 */
const LOCAL_REQUEST_TIMEOUT_MS = 0;

export class RequestTimeoutError extends Error {
  timeoutMs: number;
  constructor(timeoutMs: number) {
    super(`request timed out after ${timeoutMs}ms`);
    this.name = "RequestTimeoutError";
    this.timeoutMs = timeoutMs;
  }
}

/** 是不是打向别的机器（是的话才有「对方可能挂起」这回事） */
function isCrossMachineRequest(input: RequestInfo | URL): boolean {
  try {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    if (!raw) return false;
    return !isSameServerAsPage(raw);
  } catch {
    return false;
  }
}

function requestTimeoutMs(input: RequestInfo | URL): number {
  if (typeof AbortController === "undefined") return 0;
  return isCrossMachineRequest(input) ? NODE_REQUEST_TIMEOUT_MS : LOCAL_REQUEST_TIMEOUT_MS;
}

/**
 * 给 init 补上 deadline（跨机器才补，本机请求不动 —— 不给正常请求设人为上限）。
 *
 * 调用方已经带了 `signal` 时不再覆盖：那是它自己编排的取消语义，混进去会分不清
 * 「超时了」还是「调用方取消了」。
 *
 * 返回 deadline 元信息供调用方清理定时器 —— AbortController 没有「取消超时」的口子，
 * 定时器只能自己 clearTimeout，否则每次扇出都会漏一个 10 秒的挂单。
 */
function withRequestDeadline(
  input: RequestInfo | URL,
  init: RequestInit,
): { init: RequestInit; timeoutMs: number; timer: ReturnType<typeof setTimeout> | null; expired: () => boolean } {
  const timeoutMs =
    typeof AbortController === "undefined" || init.signal
      ? 0
      : requestTimeoutMs(input);
  if (timeoutMs <= 0) {
    return { init, timeoutMs: 0, timer: null, expired: () => false };
  }
  const controller = new AbortController();
  let expired = false;
  const timer = setTimeout(() => {
    expired = true;
    controller.abort();
  }, timeoutMs);
  return { init: { ...init, signal: controller.signal }, timeoutMs, timer, expired: () => expired };
}

/**
 * deadline 兜底生效时，把底层五花八门的网络错误统一换成可识别的 RequestTimeoutError。
 *
 * 关键是判「有没有 deadline 在管这次请求」，而不是判错误的**名字**。实测挂起时的形态
 * 因运行时而异：浏览器大多给 `AbortError: The operation was aborted due to timeout`，
 * Node/undici 给的是 `TypeError: fetch failed` 包着一个 `ConnectTimeoutError`（10 秒的
 * 连接超时和我们的 deadline 撞车，谁先到不确定）—— 只认 AbortError 会漏掉后者。
 *
 * 范围也只限「抛出来的」错误：HTTP 层的 4xx/5xx 是走 Response 回来的、根本进不到这里，
 * 所以进了这里就说明这次请求没能拿到响应，而在有 deadline 的前提下，那就是「没在时间内答话」。
 *
 * 调用方自带 signal（timeoutMs 为 0）时什么都不改 —— 那是它自己的取消语义。
 */
function normalizeAbortError(err: unknown, timeoutMs: number, expired: boolean): unknown {
  if (timeoutMs <= 0) return err;
  if (expired || (err as any)?.name === "AbortError" || !(err instanceof APIError)) {
    return new RequestTimeoutError(timeoutMs);
  }
  return err;
}

/** fetch 的统一入口：所有 protected 系列最终都过这里，deadline 只需要挂一次 */
async function fetchWithDeadline(input: RequestInfo | URL, init: RequestInit): Promise<Response> {
  const deadline = withRequestDeadline(input, init);
  try {
    return await fetch(input, deadline.init);
  } catch (err) {
    throw normalizeAbortError(err, deadline.timeoutMs, deadline.expired());
  } finally {
    if (deadline.timer) clearTimeout(deadline.timer);
  }
}

/**
 * e2eeService.protectedFetch 外面套 deadline。
 *
 * 单独一层是因为 protectedFetch 内部会**重建** init（加 proof 头、加密 body、401 时重试），
 * 我们在外层补的 signal 会被它原样带走（`{...init, method, headers}` 保留 signal），
 * 但 e2ee 那条 401 重试路径也用同一个 signal，正好一起受 deadline 约束。
 */
async function e2eeProtectedFetchWithDeadline(
  input: RequestInfo | URL,
  init: RequestInit,
): Promise<Response> {
  const deadline = withRequestDeadline(input, init);
  try {
    return await e2eeService.protectedFetch(input, deadline.init);
  } catch (err) {
    throw normalizeAbortError(err, deadline.timeoutMs, deadline.expired());
  } finally {
    if (deadline.timer) clearTimeout(deadline.timer);
  }
}

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
  const response = await fetchWithDeadline(input, init);
  const payload = await response.json().catch(() => ({} as any));
  if (!response.ok) {
    handleAccountGone(response.status, payload, input);
    throw new APIError(response.status, payload, `request failed: ${response.status}`);
  }
  return payload as T;
}

export async function fetchMaybeJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T | null> {
  const response = await fetchWithDeadline(input, init);
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
  return e2eeProtectedFetchWithDeadline(input, init);
}

// 条件请求（ETag / 304）的客户端半边。
//
// 服务端的 respondJSONList 会给列表类端点带 ETag，内容没变时回 304 ——
// 手机端最省的传输是零字节。这里负责带上 If-None-Match 并在 304 时复用上次的解析结果。
//
// 两张表按**完整 URL** 缓存（含 `user=` 与 nodeId），所以不同账户、不同节点不会串。
// 只有真发过 ETag 的端点才会写进来；其余端点没有 ETag，自然永远不进表。
//
// 上限 64：带 ETag 的端点从 3 个扩到 10 个（/api/sessions、?multi_root、tasks/overview、
// agents、task-templates、git/status、tree、tasks、replying-sessions、**单会话详情**），
// 其中几个的 URL 带会变的查询参数（tree 的 dir=、tasks 的过滤器、会话详情的
// latest= / seq=），URL 空间比原先大一个量级，
// 16 条会把 /api/sessions(111KB) 这类高频条目挤出去，反而白白丢掉命中。
// 但**仍然有界** —— git 缓存无上限正是当初崩溃的一个来源（见上游定制清单 G-AH），
// 表撑大是治「条目不够」，不是治「随便缓存」：再加端点就该重新算这个数，而不是随手调大。
//
// 注意 304 的 `response.ok` 是 **false**：必须在错误检查之前返回，否则会被当成请求失败。
const conditionalRequestMax = 64;
// 条目数管不住内存，所以再加一条**字节预算**。
//
// 起因：`/api/sessions/{key}?latest=20` 的一次回包就是 **1.14 MB**（窗口化尾部拉取，
// 实测 ~1.7 秒一次），64 条这种能把标签页直接撑爆 —— 而标签页崩溃正是这一轮要修的症状。
// 记账用原始文本长度（见 e2ee.parseProtectedJSONResponseWithSize），LRU 淘汰到预算内：
// 轮询同一个 URL 靠的就是**最近那条**留住，所以最坏情况也只是老会话出局，
// 当前看的那个始终能拿 304。
//
// 注意淘汰必须把 ETag 和载荷一起删 —— 只删载荷会留下「304 但拿不出内容」的悬空条目，
// 那正是下面 `conditional cache miss` 的分支，调用方会重试，等于白跑一次。
const conditionalRequestBytesMax = 6 * 1024 * 1024;
const conditionalETagByURL = new Map<string, string>();
const conditionalPayloadByURL = new Map<string, any>();
const conditionalBytesByURL = new Map<string, number>();
let conditionalPayloadBytes = 0;

function rememberConditionalResponse(url: string, etag: string, payload: any, bytes: number): void {
  // 同一 URL 重复写入不能重复计账 —— 先撤掉旧的。
  forgetConditionalResponse(url);
  conditionalETagByURL.set(url, etag);
  conditionalPayloadByURL.set(url, payload);
  conditionalBytesByURL.set(url, bytes);
  conditionalPayloadBytes += bytes;
  while (
    conditionalPayloadByURL.size > 0 &&
    (conditionalPayloadByURL.size > conditionalRequestMax ||
      conditionalPayloadBytes > conditionalRequestBytesMax)
  ) {
    const oldest = conditionalPayloadByURL.keys().next();
    if (oldest.done || oldest.value === undefined) break;
    // 单个条目自己就超预算时也会被淘汰：宁可那一个大端点拿不到 304，
    // 也不让「预算」变成一句空话（少传一份全量，好过把标签页撑爆）。
    forgetConditionalResponse(oldest.value);
  }
}

function forgetConditionalResponse(url: string): void {
  const bytes = conditionalBytesByURL.get(url);
  if (bytes !== undefined) {
    conditionalPayloadBytes -= bytes;
    conditionalBytesByURL.delete(url);
  }
  conditionalETagByURL.delete(url);
  conditionalPayloadByURL.delete(url);
}

export async function protectedJSON<T>(input: RequestInfo | URL, init: RequestInit = {}): Promise<T> {
  if (!protectedAPIReady()) {
    throw new Error("api_not_ready");
  }
  const url = String(input);
  const method = String(init.method || "GET").toUpperCase();
  const cacheable = method === "GET";

  let requestInit = init;
  if (cacheable) {
    const known = conditionalETagByURL.get(url);
    if (known) {
      const headers = new Headers(init.headers as HeadersInit | undefined);
      headers.set("If-None-Match", known);
      requestInit = { ...init, headers };
    }
  }

  const response = await e2eeProtectedFetchWithDeadline(input, requestInit);

  if (cacheable && response.status === 304) {
    const cached = conditionalPayloadByURL.get(url);
    if (cached !== undefined) {
      return cached as T;
    }
    // 缓存被淘汰掉了但服务端以为我们有 —— 让它当正常失败，调用方会重试。
    throw new APIError(response.status, {}, "conditional cache miss");
  }

  const parsed = await e2eeService
    .parseProtectedJSONResponseWithSize<any>(response)
    .catch(() => ({ payload: {} as any, bytes: 0 }));
  const payload = parsed.payload;
  if (!response.ok) {
    // 和其它两个 helper 一致：本机账户被删时也要登出，否则整页卡在 404。
    // （这条以前漏了，而 /api/dirs 正是走它。）
    handleAccountGone(response.status, payload, input);
    throw new APIError(response.status, payload, `request failed: ${response.status}`);
  }

  if (cacheable) {
    const etag = response.headers.get("ETag");
    if (etag) {
      rememberConditionalResponse(url, etag, payload, parsed.bytes);
    } else {
      // 端点没发 ETag：清掉可能残留的旧条目（连同它的字节账），别拿过期的 ETag 打下次请求。
      forgetConditionalResponse(url);
    }
  }
  return payload as T;
}

const NODE_RETRY_DELAYS_MS = [400, 1200];

// 每个节点按域名直连，域名可能同时挂着多个 A 记录（实测：其中一个网卡离线后地址仍被广播，
// 浏览器撞上去要等好几秒才回退）。单次失败会让该节点的项目/会话整块缺席，而重拉只在
// WS 重连或用户操作时才发生——实测能长时间不恢复。这里对同一节点补几次重试，
// 让浏览器重新建连、重新挑地址；服务端明确回 4xx 时不重试。
//
// 重试的总代价现在有上界了：每次尝试都被 fetchWithDeadline 的 10 秒 deadline 兜住
// （见 NODE_REQUEST_TIMEOUT_MS），最坏 3 次 ≈ 32 秒。此前没有 deadline 时每次尝试
// 都要挂约 2 分钟，重试反而把「快速失败」拖成了「永远不返回」。
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

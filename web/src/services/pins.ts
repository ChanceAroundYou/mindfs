// 置顶（pin）的唯一前端入口。
//
// **权威在服务端主节点**：项目置顶与会话置顶都存主节点的那张表，
// worker 上不存（也不提供这些端点）。所以本文件里的请求一律用
// controlPath 打页面服务器，**不能**用 appPath/appURL —— 那样会跟着
// 当前选中的节点走，在 PC 上置顶的结果这台机器自己都读不回来。
//
// 为什么不再用 localStorage：本地缓存只为了「先出帧」，用户真正的
// 期待是「A 设备置顶，B 设备刷新后也置顶」。缓存层没做到这件事，
// 反而制造了「清缓存就没了」的错觉 —— 现在直接以服务端为准。
//
// **不做跨设备实时推送**（用户 2026-10-04 定）：置顶变更在切换项目等导航动作
// 时顺带刷新一次，设备之间最多差一次刷新。真正的实时需要一条主节点 →
// 各 worker 浏览器的新通道，而那些连接是浏览器直连 worker 的，主节点碰不到。
import { controlPath } from "./controlPlane";
import { protectedJSON } from "./api";

// 项目置顶：键 = scopeKey（nodeID::rootID），值 = 毫秒时间戳。
export type ProjectPins = Record<string, number>;

// 会话置顶：键 = rootID::sessionKey，值 = RFC3339。
export type SessionPins = Record<string, string>;

export interface PinsSnapshot {
  projects: ProjectPins;
  sessions: SessionPins;
}

function normalizeProjects(input: unknown): ProjectPins {
  const src = input && typeof input === "object" ? (input as Record<string, unknown>) : {};
  const out: ProjectPins = {};
  for (const [key, raw] of Object.entries(src)) {
    const ts = Number(raw);
    if (key && Number.isFinite(ts) && ts > 0) out[key] = ts;
  }
  return out;
}

// 会话置顶的值是 RFC3339 字符串，这里只做「像不像时间戳」的粗校验：
// 解析交给消费方（排序时要 Date.parse），这里剔掉的只是空串与明显非字符串。
function normalizeSessions(input: unknown): SessionPins {
  const src = input && typeof input === "object" ? (input as Record<string, unknown>) : {};
  const out: SessionPins = {};
  for (const [key, raw] of Object.entries(src)) {
    if (key && typeof raw === "string" && raw.trim()) out[key] = raw.trim();
  }
  return out;
}

export function normalizePins(value: unknown): PinsSnapshot {
  const src = value && typeof value === "object" ? (value as Record<string, unknown>) : {};
  return {
    projects: normalizeProjects(src.projects ?? {}),
    sessions: normalizeSessions(src.sessions ?? {}),
  };
}

/** 读整张置顶表。主节点权威；worker 上会 403，调用方按失败处理。 */
export async function fetchPins(): Promise<PinsSnapshot> {
  return normalizePins(await protectedJSON(controlPath("/api/pins")));
}

/**
 * 置顶/取消一个项目，返回服务端落库后的整张表。
 *
 * 回整张表而不是只回这一条：置顶表的规模是几百条，一次往返省掉
 * 「本地乐观更新」与「服务端真相」两份状态的合并 —— 而那种合并正是
 * 「点了没反应 / 刷新就变回去」这类症状的来源。
 */
export async function setProjectPin(key: string, pinned: boolean): Promise<PinsSnapshot> {
  return normalizePins(
    await protectedJSON(controlPath("/api/pins/project"), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key, pinned }),
    }),
  );
}

/**
 * 置顶/取消一个会话，返回服务端落库后的整张表。
 *
 * 键在服务端拼（root_id + key），前端只传原始的两个字段 —— 键的形状
 * 是服务端的事，前端自己拼就等于有两份实现，分叉时症状是
 * 「置顶了但刷新就没」。
 */
export async function setSessionPin(
  rootId: string,
  sessionKey: string,
  pinned: boolean,
): Promise<PinsSnapshot> {
  return normalizePins(
    await protectedJSON(controlPath("/api/pins/session"), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ root_id: rootId, key: sessionKey, pinned }),
    }),
  );
}

/** 会话置顶键（与服务端 sessionPinKey 逐字节一致）。 */
export function sessionPinKey(rootId: string, sessionKey: string): string {
  const root = String(rootId || "").trim();
  const key = String(sessionKey || "").trim();
  if (!key) return "";
  return root ? `${root}::${key}` : key;
}

/**
 * 取某个项目的会话置顶键集合，给 worker 列表做叠加用。
 *
 * 为什么要叠加：worker 上 `/api/pins` 是 403（它是控制面），它的会话列表
 * 里也就没有置顶区。用户看到的置顶来自**主节点**那张表，worker 不知道，
 * 所以前端把主节点那份盖在 worker 的列表上。
 */
export function sessionPinsForRoot(pins: SessionPins, rootId: string): Map<string, string> {
  const out = new Map<string, string>();
  const root = String(rootId || "").trim();
  const prefix = root ? `${root}::` : "";
  for (const [scoped, at] of Object.entries(pins || {})) {
    if (prefix && !scoped.startsWith(prefix)) continue;
    const key = scoped.slice(prefix.length).trim();
    if (key) out.set(key, at);
  }
  return out;
}

// ─────────────────────────────────────────────────────────────────────────────
// store：置顶的唯一前端状态源
//
// 曾经有三份能重排列表的状态（列表回包的 pinnedKeys、fetchPins 的结果、
// 「快照迟到重放」），彼此无因果关系 → 每次到达都重排一次，右侧狂跳。
// 现在收成一份：**排序决策权只在这里**，列表回包不再碰排序。
//
// **不缓存**（决策 2026-10-05）。置顶是一张几十字节的偏好表，为它引入持久化
// 层不划算，而且持久化缓存天然是「双份真相」：项目置顶当初就因为住在共享偏好
// 里而跨账户泄漏，两份真相并集等于两份真相迟早分叉。所以这里只用内存 state，
// 服务端是唯一真相来源。
//
// 首帧置顶区为空、一服务端到达就排一次，是**一次**跳变（不是疯狂跳变）；用
// 缓存去换掉它，代价是永久性的双份真相。不划算。
// ─────────────────────────────────────────────────────────────────────────────

const EMPTY_SNAPSHOT: PinsSnapshot = { projects: {}, sessions: {} };

let current: PinsSnapshot = EMPTY_SNAPSHOT;
let lastFingerprint = "";
const listeners = new Set<() => void>();

/**
 * 置顶快照的指纹：只看**排序结果**。
 *
 * 为什么不是「表变没变」：反复点「置顶」不刷新时间戳（那是有意的，否则
 * 置顶顺序会被无意义的重排打乱），而项目置顶的时间戳每次都变。只比较
 * 排序后的 (键, 时间) 序列，无意义的变动就不会触发重排。
 */
export function pinsFingerprint(value: PinsSnapshot): string {
  const projects = Object.entries(value.projects || {})
    .map(([k, v]) => `${k}@${v}`)
    .sort()
    .join(",");
  const sessions = Object.entries(value.sessions || {})
    .map(([k, v]) => `${k}@${v}`)
    .sort()
    .join(",");
  return `P{${projects}}S{${sessions}}`;
}

function emit(): void {
  for (const cb of listeners) {
    try {
      cb();
    } catch {
      /* 单个订阅者出错不该影响其它 */
    }
  }
}

/** 读当前置顶（内存，恒定同步）。 */
export function readPins(): PinsSnapshot {
  return current;
}

export function subscribePins(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/**
 * 写入一份新快照。
 *
 * 返回**排序是否真的变了**：调用方据此决定要不要重排列表。指纹相同就不通知
 * 订阅者，这是消除「无意义重排」的关键 —— 远端没变的刷新、各类无关事件，
 * 都不该让列表动一下。
 */
export function writePins(next: PinsSnapshot): boolean {
  const value = normalizePins(next);
  const fingerprint = pinsFingerprint(value);
  const changed = fingerprint !== lastFingerprint;
  current = value;
  lastFingerprint = fingerprint;
  if (changed) {
    emit();
  }
  return changed;
}

/** 供测试与「换账户」时复位。 */
export function resetPinsStoreForTest(): void {
  current = EMPTY_SNAPSHOT;
  lastFingerprint = "";
  listeners.clear();
}

/**
 * 拉一次服务端快照并写入 store。
 *
 * 拉失败**不动** store：置顶是装饰性排序，一个装饰性请求失败不该把正在看的
 * 列表闪成「全不置顶」。
 */
export async function refreshPinsFromServer(): Promise<boolean> {
  return writePins(await fetchPins());
}

/**
 * 乐观更新置顶，返回回滚函数。
 *
 * 不用「本地增量 + 服务端结果合并」—— 那要合并两份状态，而合并正是
 * 「点了没反应 / 刷新就变回去」的来源。改成：先把整份表按意图改掉写进
 * store（用户立刻看到），服务端回来后整份覆盖（真相说了算）。
 */
export function optimisticProjectPin(key: string, pinned: boolean): () => void {
  const before = current;
  const next = { ...before.projects };
  if (pinned) {
    next[key] = Date.now();
  } else {
    delete next[key];
  }
  writePins({ ...before, projects: next });
  return () => writePins(before);
}

export function optimisticSessionPin(
  rootId: string,
  sessionKey: string,
  pinned: boolean,
): () => void {
  const before = current;
  const key = sessionPinKey(rootId, sessionKey);
  if (!key) return () => {};
  const next = { ...before.sessions };
  if (pinned) {
    next[key] = new Date().toISOString();
  } else {
    delete next[key];
  }
  writePins({ ...before, sessions: next });
  return () => writePins(before);
}

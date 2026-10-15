import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";
import type { Session, SessionWindow, SessionWindowMeta, SessionWindowOptions } from "./sessionTypes";

// 会话窗口拉取（方案 B）+ in-flight 去重（G-AT ①）。
//
// 从 sessionService.ts 抽出（2026-10-15）：类里只剩薄转发，调用方零改动。
// before_seq/limit 取历史窗口，latest 取尾部窗口；互斥由后端校验。
// 响应兼容顶层 `window_meta` 或嵌入 `session.window_meta`。
//
// CUSTOM(G-AT): 上游没有这层去重，同一次打开会话的两个并发调用会把几百 KB 的
// 窗口载荷各传一遍（实测每个 sessionKey 在 ?latest=N 日志里恰好出现两次）。
// in-flight 去重：同参数的并发窗口拉取合并成一次请求。
// 键含 nodeId —— 同名项目跨节点是两个不同的请求，不能互相顶掉。
// 在途表在 finally 里删，所以结算后再调用仍会重新发（去重不是缓存）。
const windowInflight = new Map<string, Promise<SessionWindow | null>>();

export async function fetchSessionWindow(
  rootId: string,
  sessionKey: string,
  opts?: SessionWindowOptions,
): Promise<SessionWindow | null> {
  try {
    const nodeId = opts?.nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    if (typeof opts?.beforeSeq === "number" && opts.beforeSeq > 0) {
      params.set("before_seq", String(opts.beforeSeq));
    }
    if (typeof opts?.latest === "number" && opts.latest > 0) {
      params.set("latest", String(opts.latest));
    }
    const limit = Number(opts?.limit || 0);
    if (limit > 0) {
      params.set("limit", String(limit));
    }
    const inflightKey = `${nodeId}::${rootId}::${sessionKey}::${opts?.beforeSeq || 0}::${opts?.latest || 0}::${limit}`;
    const existing = windowInflight.get(inflightKey);
    if (existing) {
      return existing;
    }
    const promise = (async (): Promise<SessionWindow | null> => {
      const raw = await protectedJSON<any>(
        appURL(`/api/sessions/${encodeURIComponent(sessionKey)}`, params, nodeId),
      );
      const session = (raw?.session as Session | any) || (raw as Session | any);
      const meta = (raw?.window_meta || session?.window_meta) as SessionWindowMeta;
      if (!meta) {
        console.error("[Session] getSessionWindow: missing window_meta", raw);
        return null;
      }
      return { session, meta, raw };
    })();
    windowInflight.set(inflightKey, promise);
    try {
      return await promise;
    } finally {
      windowInflight.delete(inflightKey);
    }
  } catch (err) {
    console.error("[Session] Failed to get session window:", err);
    return null;
  }
}

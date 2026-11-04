/**
 * 断线自愈：把「还没拿到会话真键的草稿」用原 request_id 重发一次。
 *
 * 为什么需要它：新会话的真键靠服务端的 `session.accepted` 回给客户端（把
 * `pending-<ts>` 草稿提升成真键）。这条回包一旦丢（fn 这类远端节点 WS 抖动、
 * 移动端 PWA 切后台必断），绑定就永远停在 `pending-*`；而发送路径把
 * `pending-*` 一律当成「没有绑定」，于是之后**每一条**消息都再开一个新会话 ——
 * 用户看到的就是「续聊老是打开新会话」。
 *
 * 服务端把 `request_id → 会话键` 记了 24 小时（ws_broadcast.go 的
 * clientRequestRecord），重复请求只回键、不重复跑回合。所以重发是安全的：
 * 原消息送达过 → 只拿到真键；没送达 → 真的发出。
 */

import type { PendingSend } from "./appSession";

export type OrphanedDraftRecoveryParams = {
  pendingRequestRef: React.RefObject<Record<string, PendingSend>>;
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  scopedRootKey: (rootId: string) => string;
  send: (draft: PendingSend) => Promise<boolean>;
};

/** 同一条草稿最多重放几次；再拿不到键就交给用户手动打开该会话来收尾。 */
export const MAX_RECOVERY_ATTEMPTS = 3;

/** 进程内计数：断线可能连续发生，不能让同一条草稿无限重放。 */
const attempts = new Map<string, number>();

/**
 * 待恢复的草稿：有待回执、没有真键、且**仍是该根当前的绑定**。
 * 绑定已经换人（用户点了别的会话/新建会话）就不再插手 —— 那是用户的选择。
 */
export function collectOrphanedDrafts(
  params: Pick<OrphanedDraftRecoveryParams, "pendingRequestRef" | "boundSessionByRootRef" | "scopedRootKey">,
): PendingSend[] {
  const out: PendingSend[] = [];
  const pendingMap = params.pendingRequestRef.current || {};
  for (const draft of Object.values(pendingMap)) {
    if (!draft?.requestId || draft.sessionKey || !draft.tempKey) continue;
    if (params.boundSessionByRootRef.current[params.scopedRootKey(draft.rootId)] !== draft.tempKey) {
      continue;
    }
    out.push(draft);
  }
  return out;
}

/**
 * 重放所有孤儿草稿。返回这次真正重发出去的，以及已经放弃的（调用方据此给用户一次提示）。
 * 不做去抖：调用点只在 WS 连接建立时触发。
 */
export async function recoverOrphanedDrafts(
  params: OrphanedDraftRecoveryParams,
): Promise<{ retried: PendingSend[]; gaveUp: PendingSend[] }> {
  const retried: PendingSend[] = [];
  const gaveUp: PendingSend[] = [];
  for (const draft of collectOrphanedDrafts(params)) {
    const requestId = String(draft.requestId);
    const used = attempts.get(requestId) || 0;
    if (used >= MAX_RECOVERY_ATTEMPTS) {
      gaveUp.push(draft);
      continue;
    }
    attempts.set(requestId, used + 1);
    console.warn("[session/send] draft_recovery replay", {
      rootId: draft.rootId,
      requestId,
      tempKey: draft.tempKey,
      attempt: used + 1,
    });
    const sent = await params.send(draft).catch(() => false);
    if (sent) {
      retried.push(draft);
    }
  }
  return { retried, gaveUp };
}

/** 测试/会话重置用：清掉计数，避免同一 requestId 在长会话里被永久放弃。 */
export function resetRecoveryAttempts(): void {
  attempts.clear();
}

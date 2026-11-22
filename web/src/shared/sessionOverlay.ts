/** Minimal exchange shape needed by the session window overlay. */
export type OverlayExchange = {
  seq?: number | string | null;
  role?: string;
  content?: string;
  mindfs_id?: string;
} & Record<string, unknown>;

export interface TailOverlayInput {
  /** App cache, including persisted rows and in-flight entries. */
  exchanges?: readonly OverlayExchange[] | null;
  /** Persisted rows currently rendered by the window. */
  visibleExchanges?: readonly OverlayExchange[] | null;
}

const seqOf = (ex: OverlayExchange | undefined): number => Number(ex?.seq || 0);
const roleOf = (ex: OverlayExchange | undefined): string =>
  String(ex?.role || "").toLowerCase();
const mindfsIDOf = (ex: OverlayExchange | undefined): string =>
  String(ex?.mindfs_id || "").trim();

const timeOf = (ex: OverlayExchange | undefined): number => {
  const parsed = Date.parse(String((ex as Record<string, unknown> | undefined)?.timestamp || ""));
  return Number.isFinite(parsed) ? parsed : 0;
};

/** 判断「残留尾巴」时容忍的时钟偏差：客户端比服务端快/慢一分钟以内都不算陈旧。 */
const STALE_TAIL_GRACE_MS = 60_000;

const roleCategory = (role: string): string => {
  if (role === "user") return "user";
  if (role === "tool") return "tool";
  if (
    role === "agent" ||
    role === "assistant" ||
    role === "thought" ||
    role === "plan" ||
    role === "todo" ||
    role === "compact"
  ) {
    return "agent";
  }
  return role;
};

const identityOf = (ex: OverlayExchange): string => {
  const mindfsID = mindfsIDOf(ex);
  if (!mindfsID) return "";
  return JSON.stringify([roleCategory(roleOf(ex)), mindfsID]);
};

/**
 * Return cached rows the active window does not already render.
 *
 * G-BH: persisted rows are reconciled by seq; transient rows are reconciled
 * only by MindFS identity and role category. Text similarity is not identity.
 * Tool cards retain their own callId reconciliation in the timeline.
 */
export function computeTailOverlay(input: TailOverlayInput): OverlayExchange[] {
  const exchanges = Array.isArray(input.exchanges) ? input.exchanges : [];
  const visible = Array.isArray(input.visibleExchanges) ? input.visibleExchanges : [];
  const visibleSeqs = new Set<number>();
  const renderedIdentities = new Set<string>();

  for (const ex of [...visible, ...exchanges]) {
    if (seqOf(ex) > 0) {
      const identity = identityOf(ex);
      if (identity) renderedIdentities.add(identity);
    }
  }
  for (const ex of visible) {
    const seq = seqOf(ex);
    if (seq > 0) visibleSeqs.add(seq);
  }

  // 本轮起点＝缓存里**最新一条持久化的 user 行**。排它之前、又确实更旧的瞬时行不是
  // 活尾巴，而是上一轮遗留在缓存里的续写行：CLI 在 result 之后还会继续输出（后台任务
  // 结果回注等），这些行没有新的 user 行领着、轮末也不会落盘，于是永久留在尾巴上。
  // 一旦把它们整体当 tail 追加到窗口之后，用户刚发的新消息就会被顶到它们上面
  // （实测 llm-deploy 会话：06:35 的续写行排到了 06:54 的用户消息之后）。
  //
  // 为什么不只看位置：持久化的 user 行偶发晚到（合并 miss 时会被追加到缓存末尾），
  // 那时本轮的瞬时行反而排在它前面。时间判据能认出「它们比边界新」，不误杀。
  // 容忍 60s 时钟偏差：宁可让一分钟以内的旧行继续显示，也不误杀正在流式输出的行。
  let boundaryIndex = -1;
  let boundaryTime = 0;
  for (let i = 0; i < exchanges.length; i += 1) {
    const ex = exchanges[i];
    if (seqOf(ex) <= 0 || roleOf(ex) !== "user") continue;
    const t = timeOf(ex);
    if (t >= boundaryTime) {
      boundaryIndex = i;
      boundaryTime = t;
    }
  }
  const isStaleTail = (ex: OverlayExchange, index: number): boolean => {
    if (boundaryIndex < 0 || boundaryTime <= 0 || index >= boundaryIndex) return false;
    const t = timeOf(ex);
    return t > 0 && t + STALE_TAIL_GRACE_MS < boundaryTime;
  };

  const out: OverlayExchange[] = [];
  for (let index = 0; index < exchanges.length; index += 1) {
    const ex = exchanges[index];
    if (seqOf(ex) > 0) {
      if (!visibleSeqs.has(seqOf(ex))) out.push(ex);
      continue;
    }
    if (isStaleTail(ex, index)) continue;

    // Tool cards have their own callId reconciliation in the timeline.
    if (roleOf(ex) === "tool") {
      out.push(ex);
      continue;
    }

    const identity = identityOf(ex);
    if (identity && renderedIdentities.has(identity)) continue;
    out.push(ex);
  }
  return out;
}

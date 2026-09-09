// 窗口化视图（方案 B）的流式尾部合并。
//
// 旧逻辑只认 seq > windowMeta.maxSeq，而流式期间的交换没有 seq（appendAgentChunk* 直接
// push，无 seq），导致长会话（hasMore=true）生成过程中用户消息/thinking/文本被挡在可视
// 窗口外，结束后又不重拉窗口，只能手动同步才见（2026-09-09 排查，commit ebcfc39 引入的
// 窗口化回归）。
export type MergeableExchange = { seq?: number } & Record<string, unknown>;

const seqOf = (exchange: MergeableExchange): number =>
  Number(exchange?.seq || 0);

// 持久化增量（seq>0 且 > maxSeq，如重连回放带来的补齐）按 seq 归并进窗口；
// 瞬时尾部（seq=0）整体替换到末尾，保持 App 缓存内的天然顺序（user → thought → agent）。
export function mergeWindowedTail(
  prev: MergeableExchange[],
  incoming: MergeableExchange[],
  maxSeq: number,
): { exchanges: MergeableExchange[]; changed: boolean } {
  const persistedPrev = (prev || []).filter((e) => seqOf(e) > 0);
  const seen = new Set(persistedPrev.map(seqOf));
  const persistedFresh = (incoming || []).filter((e) => {
    const s = seqOf(e);
    return s > 0 && s > maxSeq && !seen.has(s);
  });
  const transient = (incoming || []).filter((e) => seqOf(e) === 0);
  if (persistedFresh.length === 0 && transient.length === 0) {
    return { exchanges: prev || [], changed: false };
  }
  const persisted = persistedFresh.length
    ? [...persistedPrev, ...persistedFresh].sort((a, b) => seqOf(a) - seqOf(b))
    : persistedPrev;
  return {
    exchanges: [...persisted, ...transient] as MergeableExchange[],
    changed: true,
  };
}

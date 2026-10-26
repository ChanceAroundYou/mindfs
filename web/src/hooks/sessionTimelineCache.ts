import { groupConsecutiveToolCalls } from "./sessionTimeline";

/**
 * 增量构建：逐 exchange 取缓存分片，拼接后整体跑一次 groupConsecutiveToolCalls。
 * cache 由调用方持有（useRef），跨渲染复用。
 *
 * id 里的下标必须是**全数组位置**（`buildTimelineItems` 的 startIndex 参数）——
 * 每片传单元素数组时分片内下标恒为 0，会造出 N 个 `user:0…` / `assistant:0:0`，
 * React key 与虚拟器 getItemKey 撞键，测量缓存把所有行定位到同一个 start，
 * 视觉上就是大面积压叠（2026-10-26 真机复现，G-KC 引入）。
 */
export function buildTimelineIncremental(
  exchanges: ExchangeLike[],
  exchangeAux: ExchangeAuxMapLike,
  blocks: SessionBlockMapLike,
  cache: TimelineChunkCache,
): TimelineItem[] {
  cache.resetTouched();
  const chunks: TimelineItem[] = [];
  for (let i = 0; i < exchanges.length; i += 1) {
    const ex = exchanges[i];
    chunks.push(...cache.itemsFor(ex, exchangeAux, blocks, i));
  }
  cache.retainUntouched();
  return groupConsecutiveToolCalls(chunks);
}
//
// 背景：buildBaseTimeline 每次都对整份 exchanges 重跑 —— 每条 seq 要解析块流、
// 按 pos 排序、拼文本、哈希 id。上翻一页（~8 条新 exchange）却把整份历史都重建一遍，
// 是「上翻卡」的主体。这里按 exchange 缓存分片：同一 ex/aux/blocks 引用未变就复用，
// 重活只发生在新 exchange 上。groupConsecutiveToolCalls 仍在拼接结果上跑一遍（跨 exchange
// 的连续同类工具卡仍需合并），但那是廉价的单遍扫描。
import type { ExchangeLike, TimelineItem } from "./sessionTimeline";
import type { ExchangeAuxMapLike } from "./sessionTimeline";
import type { SessionBlockMapLike } from "./sessionBlocks";
import { buildTimelineItems } from "./sessionTimeline";

export type TimelineChunkKey = string;

export type TimelineChunkCache = {
  itemsFor(
    ex: ExchangeLike,
    aux: ExchangeAuxMapLike,
    blocks: SessionBlockMapLike,
    /** 该 exchange 在**全数组**里的位置（id 下标与瞬时行缓存键用）。 */
    index: number,
  ): TimelineItem[];
  retainUntouched(): void;
  resetTouched(): void;
};

function depsChanged(
  cached: { ex: ExchangeLike; aux: ExchangeAuxMapLike | undefined; blocks: SessionBlockMapLike | undefined },
  ex: ExchangeLike,
  aux: ExchangeAuxMapLike | undefined,
  blocks: SessionBlockMapLike | undefined,
): boolean {
  return cached.ex !== ex || cached.aux !== aux || cached.blocks !== blocks;
}

export function createTimelineChunkCache(): TimelineChunkCache {
  const store = new Map<TimelineChunkKey, { ex: ExchangeLike; aux: ExchangeAuxMapLike | undefined; blocks: SessionBlockMapLike | undefined; items: TimelineItem[] }>();
  const touched = new Set<TimelineChunkKey>();
  return {
    itemsFor(ex, aux, blocks, index) {
      const seq = Number(ex.seq || 0);
      // 缓存键：持久行按 seq；瞬时行按全数组位置 + 对象身份。
      // id 里嵌的是 exchange 在全数组里的下标，分片内下标恒为 0 —— 键和 id
      // 都不能用分片内下标，否则 N 片撞成同一个 id（2026-10-26 压叠事故）。
      const key: TimelineChunkKey = seq > 0 ? `s${seq}` : `t:${index}:${transientId(ex)}`;
      const relevantAux = seq > 0 ? (aux[String(seq)] ? { [String(seq)]: aux[String(seq)] } : {}) : {};
      // For persisted rows, only the relevant seq's aux/blocks matter.
      const relevantBlocks = seq > 0 ? (blocks[String(seq)] ? { [String(seq)]: blocks[String(seq)] } : {}) : {};
      const cached = store.get(key);
      if (cached && !depsChanged(cached, ex, relevantAux, relevantBlocks)) {
        return cached.items;
      }
      // 单 exchange 分片：把 aux/blocks 裁成只含本 seq 的，避免缓存过量。
      // index 传全数组位置，id 下标与整批构建逐字一致。
      const items = buildTimelineItems([ex], seq > 0 ? relevantAux : {}, seq > 0 ? relevantBlocks : {}, index);
      // buildBaseTimeline 对单个 exchange 仍可能产出多条（assistant 多段）；保留。
      touched.add(key);
      store.set(key, { ex, aux: relevantAux, blocks: relevantBlocks, items });
      return items;
    },
    retainUntouched() {
      for (const k of store.keys()) {
        if (!touched.has(k)) store.delete(k);
      }
    },
    resetTouched() {
      touched.clear();
    },
  };
}

// 瞬时行对象身份 → 字符串键（WeakMap 保证同一对象同键、GC 后释放）。
const transientIds = new WeakMap<object, string>();
let transientCounter = 0;
function transientId(ex: ExchangeLike): string {
  if (typeof ex !== "object" || ex === null) return String(transientCounter++);
  let id = transientIds.get(ex as object);
  if (!id) {
    id = String(++transientCounter);
    transientIds.set(ex as object, id);
  }
  return id;
}

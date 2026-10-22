import { useCallback } from "react";
import type { Dispatch, SetStateAction } from "react";
import type { ExchangeAux, SessionBlock, SessionWindowMeta } from "../../shared/sessionTypes";
import type { ExchangeArray } from "./sessionViewerTypes";

/**
 * 窗口结果并入可见状态（方案 B）。
 *
 * 从 SessionViewer 抽出（2026-10-22）：这段只认「窗口回包 + 三个 setter + windowMeta」，
 * 与渲染、滚动、虚拟化都无关；留在宿主里会把 500 行硬线顶穿（G-JT 落地时正好 505 行）。
 *
 * `keepOlder` 的语义是**只增不减**：窗口只覆盖 [minSeq, maxSeq]，比 minSeq 更老的首帧
 * 种子 / loadMore 翻出来的历史必须保住 —— 整体替换会把它们扔掉，用户看到「刚加载出来的
 * 历史闪一下又没了」，顶部哨兵再次可见又触发 loadMore，形成无限抖动。
 */
export function useSessionWindowApply({
  mergeVisibleExchanges,
  setVisibleExchanges,
  setVisibleAux,
  setVisibleBlocks,
  setWindowMeta,
}: {
  mergeVisibleExchanges: (previous: unknown[], incoming: unknown[]) => unknown[];
  setVisibleExchanges: Dispatch<SetStateAction<ExchangeArray>>;
  setVisibleAux: Dispatch<SetStateAction<Record<string, ExchangeAux[]>>>;
  setVisibleBlocks: Dispatch<SetStateAction<Record<string, SessionBlock[]>>>;
  setWindowMeta: Dispatch<SetStateAction<SessionWindowMeta>>;
}) {
  return useCallback(
    (
      res: { session: any; meta: SessionWindowMeta } | null,
      opts?: { keepOlder?: boolean },
    ) => {
      if (!res) return false;
      const winSession = res.session as any;
      const winExchanges = Array.isArray(winSession?.exchanges)
        ? (winSession.exchanges as ExchangeArray)
        : [];
      const winAux =
        (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
      const winBlocks =
        (winSession?.blocks as Record<string, SessionBlock[]>) || {};
      setVisibleExchanges((prev) => {
        if (!opts?.keepOlder) return winExchanges;
        return mergeVisibleExchanges(prev, winExchanges) as ExchangeArray;
      });
      setVisibleAux((prev) => (opts?.keepOlder ? { ...prev, ...winAux } : winAux));
      // 块与 aux 同一合并语义：整体替换会把种子/翻出来的历史那部分块扔掉（正文闪一下又没了）。
      setVisibleBlocks((prev) =>
        opts?.keepOlder ? { ...prev, ...winBlocks } : winBlocks,
      );
      // minSeq 必须报**实际可见的最小 seq**：keepOlder 合并进来的种子可能比窗口更老，
      // 用窗口的 minSeq 会让下一次 loadMore 去取已经有的行（渲染两遍）。
      setWindowMeta((prevMeta) => {
        const winMinSeq = Number(res.meta?.minSeq || 0);
        if (!opts?.keepOlder || !winMinSeq) return res.meta;
        return {
          ...res.meta,
          minSeq: Math.min(winMinSeq, Number(prevMeta?.minSeq || 0) || winMinSeq),
        };
      });
      return true;
    },
    [mergeVisibleExchanges, setVisibleAux, setVisibleBlocks, setVisibleExchanges, setWindowMeta],
  );
}

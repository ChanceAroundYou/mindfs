import { useEffect } from "react";
import type { Dispatch, MutableRefObject, RefObject, SetStateAction } from "react";
import {
  SESSION_WINDOW_SIZE,
  getSessionWindow,
  type ExchangeAux,
  type SessionWindowMeta,
} from "../../services/session";
import type { TimelineItem } from "../../hooks/useSessionStream";
import type { ExchangeArray } from "./sessionViewerTypes";

export type TargetSeqScrollCtx = {
  rootId?: string | null;
  sessionKey: string | null;
  sessionNodeId: string | undefined;
  targetSeq?: number;
  targetSeqRequestKey?: string | number;
  timeline: TimelineItem[];
  windowMeta: SessionWindowMeta;
  activeScrollRef: RefObject<HTMLDivElement | null>;
  targetSeqScrollKeyRef: MutableRefObject<string>;
  shouldStickToBottomRef: MutableRefObject<boolean>;
  targetSeqFrameRef: MutableRefObject<number | null>;
  targetSeqTimerRefs: MutableRefObject<number[]>;
  cancelTargetSeqScroll: () => void;
  setVisibleExchanges: Dispatch<SetStateAction<ExchangeArray>>;
  setVisibleAux: Dispatch<SetStateAction<Record<string, ExchangeAux[]>>>;
  setWindowMeta: Dispatch<SetStateAction<SessionWindowMeta>>;
};

/**
 * 「跳到某一条」的定位逻辑：targetSeq 已在窗口内就直接滚过去（并按 80/220/480ms
 * 三次补滚，等工具卡展开把行高撑完）；不在窗口内就先拉一个以它为中心的窗口再滚。
 *
 * 从 SessionViewer 抽出（2026-10-08）：这 107 行只认「目标 seq + 滚动容器 + 三个
 * 定位计时器 ref + 三个窗口 setter」，不读时间线内容也不管首帧加载 —— 宿主里那些
 * 依赖数组和 refs 因此短了一截。
 */
export function useTargetSeqScroll(ctx: TargetSeqScrollCtx) {
  const {
    rootId,
    sessionKey,
    sessionNodeId,
    targetSeq,
    targetSeqRequestKey,
    timeline,
    windowMeta,
    activeScrollRef,
    targetSeqScrollKeyRef,
    shouldStickToBottomRef,
    targetSeqFrameRef,
    targetSeqTimerRefs,
    cancelTargetSeqScroll,
    setVisibleExchanges,
    setVisibleAux,
    setWindowMeta,
  } = ctx;

  useEffect(() => {
    if (!targetSeq) {
      targetSeqScrollKeyRef.current = "";
      return;
    }
    if (!sessionKey) {
      return;
    }
    const scrollKey = `${sessionKey || ""}:${targetSeq}:${targetSeqRequestKey}`;
    if (targetSeqScrollKeyRef.current === scrollKey) {
      return;
    }
    const container = activeScrollRef?.current;
    if (!container || !timeline.length) {
      return;
    }
    const node = container.querySelector<HTMLElement>(
      `[data-session-seq="${targetSeq}"]`,
    );
    const inRange =
      windowMeta.minSeq > 0 &&
      targetSeq >= windowMeta.minSeq &&
      targetSeq <= windowMeta.maxSeq;
    if (node && inRange) {
      targetSeqScrollKeyRef.current = scrollKey;
      shouldStickToBottomRef.current = false;
      cancelTargetSeqScroll();
      const scrollToNode = () => {
        const latestContainer = activeScrollRef?.current;
        const latestNode = latestContainer?.querySelector<HTMLElement>(
          `[data-session-seq="${targetSeq}"]`,
        );
        if (!latestContainer || !latestNode) {
          return;
        }
        shouldStickToBottomRef.current = false;
        const nextTop = Math.max(
          0,
          latestNode.offsetTop -
            latestContainer.clientHeight / 2 +
            latestNode.offsetHeight / 2,
        );
        latestContainer.scrollTo({ top: nextTop, behavior: "auto" });
      };
      targetSeqFrameRef.current = window.requestAnimationFrame(() => {
        targetSeqFrameRef.current = window.requestAnimationFrame(() => {
          targetSeqFrameRef.current = null;
          scrollToNode();
        });
      });
      [80, 220, 480].forEach((delay) => {
        const timer = window.setTimeout(() => {
          targetSeqTimerRefs.current = targetSeqTimerRefs.current.filter(
            (item) => item !== timer,
          );
          if (targetSeqScrollKeyRef.current !== scrollKey) {
            return;
          }
          scrollToNode();
        }, delay);
        targetSeqTimerRefs.current.push(timer);
      });
      return;
    }
    // 跨窗口：targetSeq 不在当前可见窗口内，拉取以 targetSeq 为中心的窗口再滚动。
    if (targetSeq > 0 && !inRange) {
      targetSeqScrollKeyRef.current = scrollKey;
      shouldStickToBottomRef.current = false;
      cancelTargetSeqScroll();
      getSessionWindow(rootId || "", sessionKey, {
          beforeSeq: targetSeq + Math.floor(SESSION_WINDOW_SIZE / 2),
          limit: SESSION_WINDOW_SIZE,
          nodeId: sessionNodeId,
        })
        .then((res) => {
          if (!res) return;
          const winSession = res.session as any;
          const winExchanges = Array.isArray(winSession?.exchanges)
            ? (winSession.exchanges as ExchangeArray)
            : [];
          const winAux =
            (winSession?.exchange_aux as Record<string, ExchangeAux[]>) || {};
          setVisibleExchanges(winExchanges);
          setVisibleAux(winAux);
          setWindowMeta(res.meta);
          window.requestAnimationFrame(() => {
            const el = activeScrollRef?.current;
            const targetNode = el?.querySelector<HTMLElement>(
              `[data-session-seq="${targetSeq}"]`,
            );
            if (el && targetNode) {
              shouldStickToBottomRef.current = false;
              el.scrollTo({
                top: Math.max(
                  0,
                  targetNode.offsetTop -
                    el.clientHeight / 2 +
                    targetNode.offsetHeight / 2,
                ),
                behavior: "auto",
              });
            }
          });
        })
        .catch(() => {});
    }
  }, [sessionKey, targetSeq, targetSeqRequestKey, timeline, windowMeta]);
}

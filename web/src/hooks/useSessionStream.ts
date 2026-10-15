import { useEffect, useMemo, useRef, useState } from "react";
import { sessionService } from "../services/session";
import { translateNow } from "../i18n";
import type { SessionBlockMapLike } from "./sessionBlocks";
import {
  applySessionContextWindow,
  buildBaseTimeline,
  dedupeToolCards,
  settleRunningTools,
  type ContextWindowLike,
  type ExchangeAuxMapLike,
  type ExchangeLike,
  type UseSessionStreamResult,
} from "./sessionTimeline";

export { groupConsecutiveToolCalls } from "./sessionTimeline";
export type { TimelineItem } from "./sessionTimeline";

export function useSessionStream(
  sessionKey: string | null,
  exchanges: ExchangeLike[] = [],
  exchangeAux: ExchangeAuxMapLike = {},
  sessionContextWindow?: ContextWindowLike,
  sessionPending = false,
  blocks: SessionBlockMapLike = {},
): UseSessionStreamResult {
  const [isStreaming, setIsStreaming] = useState(false);
  const [streamVersion, setStreamVersion] = useState(0);
  const [streamStatusText, setStreamStatusText] = useState("");
  // 流式 chunk（message_chunk/thought_chunk）高频到达时合并 streamVersion 更新，
  // 避免每 chunk 一次 setState → SessionViewer 重渲染风暴（与 App.tsx 的
  // bumpCacheVersionDebounced 同思路）。滚动跟随用 30ms 粒度视觉无差异。
  const chunkVersionTimerRef = useRef<number | null>(null);
  const bumpStreamVersion = () => {
    if (chunkVersionTimerRef.current !== null) return;
    chunkVersionTimerRef.current = window.setTimeout(() => {
      chunkVersionTimerRef.current = null;
      setStreamVersion((value) => value + 1);
    }, 30);
  };

  const baseTimeline = useMemo(
    () =>
      applySessionContextWindow(
        buildBaseTimeline(exchanges, exchangeAux, blocks),
        sessionContextWindow,
      ),
    [exchanges, exchangeAux, blocks, sessionContextWindow],
  );

  useEffect(() => {
    setStreamVersion(0);
    setStreamStatusText("");
    if (!sessionKey) {
      setIsStreaming(false);
      return;
    }
    setIsStreaming(
      sessionPending && sessionService.dispatcher.isSessionStreaming(sessionKey),
    );

    const unsubscribe = sessionService.dispatcher.subscribe(sessionKey, {
      onStream: (event) => {
        if (event.type === "message_chunk" || event.type === "thought_chunk") {
          bumpStreamVersion();
        } else {
          setStreamVersion((value) => value + 1);
        }
        if (event.type === "recovery") {
          setStreamStatusText(event.data?.message || translateNow("session.recovering"));
          setIsStreaming(true);
          return;
        }
        if (event.type === "message_chunk") {
          setStreamStatusText("");
        }
        if (event.type === "message_done") {
          // 只完成一条 agent 消息；回合仍由带 turn identity 的 session.done 终止。
          setStreamStatusText("");
          return;
        }
        if (event.type === "error") {
          setStreamStatusText("");
          setIsStreaming(false);
        } else {
          setIsStreaming(true);
        }
      },
      onDone: () => {
        setStreamStatusText("");
        setIsStreaming(false);
      },
      onError: () => {
        setStreamStatusText("");
        setIsStreaming(false);
      },
    });

    return () => {
      unsubscribe();
      if (chunkVersionTimerRef.current !== null) {
        window.clearTimeout(chunkVersionTimerRef.current);
        chunkVersionTimerRef.current = null;
      }
    };
  }, [sessionKey, sessionPending]);

  // Viewport-only rerenders must not look like new session content to the
  // viewer's tail-following effect. dedupeToolCards 在 settleRunningTools 之后：
  // 先把 running 收成终态，再折叠重复 tool 卡片，顺序反了会漏掉仍在跑的那张。
  const settledTimeline = useMemo(
    () => dedupeToolCards(settleRunningTools(baseTimeline)),
    [baseTimeline],
  );

  return {
    timeline: settledTimeline,
    // 本地流式标记只是「本回合已经有内容流进来了」的**子状态**，不能独立于回合本身存在。
    // 它只由 WS 事件清（done/message_done/error），没有轮询对账 —— 丢一条 `session.done`
    // 就永久为真，于是列表蓝灯（pending 派生、有 5s 对账）已灭而流里「正在生成」不灭，
    // 最后一条的时间戳也一直藏着。把闸门收在这里（唯一真相 `sessionPending`），
    // 三个指示器就不可能互相矛盾（G-AY 纯派生的补完）。
    isStreaming: isStreaming && !!sessionPending,
    streamVersion,
    streamStatusText,
  };
}

import { useEffect, useMemo, useRef, useState } from "react";
import { sessionService, isPersistedSeq } from "../services/session";
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

function splitExchanges(exchanges: ExchangeLike[]) {
  const persisted: ExchangeLike[] = [];
  const transient: ExchangeLike[] = [];
  for (const exchange of exchanges) {
    if (isPersistedSeq(exchange.seq)) persisted.push(exchange);
    else transient.push(exchange);
  }
  return { persisted, transient };
}

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
  const chunkVersionTimerRef = useRef<number | null>(null);
  const bumpStreamVersion = () => {
    if (chunkVersionTimerRef.current !== null) return;
    chunkVersionTimerRef.current = window.setTimeout(() => {
      chunkVersionTimerRef.current = null;
      setStreamVersion((value) => value + 1);
    }, 30);
  };

  const split = splitExchanges(exchanges);
  const persistedRef = useRef<ExchangeLike[]>([]);
  const persistedExchanges =
    persistedRef.current.length === split.persisted.length &&
    persistedRef.current.every((item, i) => item === split.persisted[i])
      ? persistedRef.current
      : (persistedRef.current = split.persisted);
  // 持久部分缓存：仅当持久 exchanges / aux / blocks / contextWindow 变化时重建。
  const persistedCacheRef = useRef<{ key: unknown[]; value: any } | null>(null);
  const persistedKey = [persistedExchanges, exchangeAux, blocks, sessionContextWindow];
  const persistedChanged =
    !persistedCacheRef.current ||
    persistedCacheRef.current.key.length !== persistedKey.length ||
    !persistedCacheRef.current.key.every((v, i) => v === persistedKey[i]);
  if (persistedChanged) {
    persistedCacheRef.current = {
      key: persistedKey,
      value: applySessionContextWindow(buildBaseTimeline(persistedExchanges, exchangeAux, blocks), sessionContextWindow),
    };
  }
  const persistedBaseTimeline = persistedCacheRef.current!.value;

  const transientTimeline = useMemo(
    () => buildBaseTimeline(split.transient, {}, {}),
    [split.transient],
  );
  const baseTimeline = useMemo(
    () => [...persistedBaseTimeline, ...transientTimeline],
    [persistedBaseTimeline, transientTimeline],
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
          setIsStreaming(!!sessionPending);
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

  const settledTimeline = useMemo(
    () => dedupeToolCards(settleRunningTools(baseTimeline)),
    [baseTimeline],
  );

  return {
    timeline: settledTimeline,
    isStreaming: isStreaming && !!sessionPending,
    streamVersion,
    streamStatusText,
  };
}

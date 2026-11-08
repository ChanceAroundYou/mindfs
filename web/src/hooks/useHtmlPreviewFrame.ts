import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { RefObject, SyntheticEvent } from "react";
import { useRawAssetUrl } from "./useRawAssetUrl";
import { findUnrunnableScripts } from "../services/htmlPreview";

/**
 * HTML 预览的运行态诊断（logic 层）：URL 构造、开发入口判定、白屏原因收集。
 *
 * 自 2026-11-09 起预览页与 mindfs 同源（iframe sandbox 与响应头 CSP 都带
 * allow-same-origin），所以这里可以直接读 contentWindow 挂错误监听、判断是否空白。
 * 万一被浏览器拒绝（跨源读不到），全部 try/catch 掉 —— 诊断失败不能拖垮预览本身。
 *
 * 解析期抛的错（最典型的白屏原因）在 iframe 的 load 之前就发生了，所以后端还会在
 * 每份预览 HTML 里注入一段探针，用 postMessage 把错误回传；这里按 event.source 校验
 * 来源后收下。两路（探针 + load 后直挂监听）重复的消息会去重。
 */

/** 空白判定：没有任何文字，也没有图片/画布/媒体/表单这类可见元素。 */
function documentLooksBlank(doc: Document): boolean {
  const body = doc.body;
  if (!body) {
    return true;
  }
  if ((body.innerText || "").trim()) {
    return false;
  }
  return !body.querySelector("img, svg, canvas, video, iframe, table, input, select, textarea, button");
}

function describeReason(reason: unknown): string {
  if (reason instanceof Error) {
    return reason.message || reason.name;
  }
  if (typeof reason === "string") {
    return reason;
  }
  try {
    return JSON.stringify(reason);
  } catch {
    return String(reason);
  }
}

const BLANK_CHECK_DELAY_MS = 1200;
const MAX_PROBLEMS = 5;
const PROBE_MESSAGE_KEY = "__mindfsHtmlPreviewError";

export type HtmlPreviewFrame = {
  url: string;
  unrunnableScripts: string[];
  problems: string[];
  blank: boolean;
  frameRef: RefObject<HTMLIFrameElement | null>;
  handleFrameLoad: (event: SyntheticEvent<HTMLIFrameElement>) => void;
};

export function useHtmlPreviewFrame(params: {
  root?: string;
  path: string;
  version?: string;
  content?: string;
}): HtmlPreviewFrame {
  const { root, path, version, content } = params;
  const url = useRawAssetUrl(root, path, undefined, version);
  const unrunnableScripts = useMemo(() => findUnrunnableScripts(content), [content]);
  const [problems, setProblems] = useState<string[]>([]);
  const [blank, setBlank] = useState(false);
  const blankTimerRef = useRef<number | null>(null);
  const frameRef = useRef<HTMLIFrameElement | null>(null);

  const pushProblem = useCallback((message: string) => {
    const trimmed = String(message || "").trim();
    if (!trimmed) {
      return;
    }
    setProblems((prev) => (prev.includes(trimmed) ? prev : [...prev, trimmed].slice(0, MAX_PROBLEMS)));
  }, []);

  const clearBlankTimer = useCallback(() => {
    if (blankTimerRef.current !== null) {
      window.clearTimeout(blankTimerRef.current);
      blankTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    setProblems([]);
    setBlank(false);
    clearBlankTimer();
    return clearBlankTimer;
  }, [url, clearBlankTimer]);

  // 探针回传：只认当前 iframe 发来的消息。
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const frame = frameRef.current;
      if (!frame || event.source !== frame.contentWindow) {
        return;
      }
      const data = event.data as Record<string, unknown> | null;
      if (!data || typeof data !== "object" || typeof data[PROBE_MESSAGE_KEY] !== "string") {
        return;
      }
      pushProblem(data[PROBE_MESSAGE_KEY] as string);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [pushProblem]);

  const handleFrameLoad = useCallback((event: SyntheticEvent<HTMLIFrameElement>) => {
    const frame = event.currentTarget;
    setBlank(false);
    clearBlankTimer();

    let win: Window | null = null;
    let doc: Document | null = null;
    try {
      win = frame.contentWindow;
      doc = frame.contentDocument;
    } catch {
      return;
    }
    if (!win || !doc) {
      return;
    }

    try {
      // 捕获阶段能同时收到脚本异常与子资源（script/link/img）加载失败。
      win.addEventListener(
        "error",
        (event: Event) => {
          const errorEvent = event as ErrorEvent;
          if (errorEvent.message) {
            pushProblem(errorEvent.message);
            return;
          }
          const target = event.target as HTMLElement | null;
          if (target && target.tagName && target !== (win as unknown as HTMLElement)) {
            const tag = target.tagName.toLowerCase();
            const src = (target as HTMLScriptElement).src || (target as HTMLLinkElement).href || "";
            pushProblem(src ? `${tag}: ${src}` : `${tag}: failed to load`);
          }
        },
        true,
      );
      win.addEventListener("unhandledrejection", (event: Event) => {
        pushProblem(describeReason((event as PromiseRejectionEvent).reason));
      });
    } catch {
      return;
    }

    blankTimerRef.current = window.setTimeout(() => {
      blankTimerRef.current = null;
      try {
        if (doc && documentLooksBlank(doc)) {
          setBlank(true);
        }
      } catch {
        /* 读不到就放弃空白判定 */
      }
    }, BLANK_CHECK_DELAY_MS);
  }, [clearBlankTimer, pushProblem]);

  return { url, unrunnableScripts, problems, blank, frameRef, handleFrameLoad };
}

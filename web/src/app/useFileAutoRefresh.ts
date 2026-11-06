/**
 * 当前文件的自动刷新（2026-11-06）。
 *
 * 覆盖「编辑/重新生成同名文件后，打开的还是旧版」的两种漏法：
 *   - 拉取：切到会话再切回来、或别的节点/终端改了文件，当前预览不会自己重取；
 *   - 缓存：memory/IndexedDB 与浏览器启发式缓存都可能还给旧内容。
 *
 * 三条通道，按付费/时延从低到高：
 *   1. WS `file.changed`（后端 fsnotify）—— 主路径，看到就重取；
 *   2. 这里：可见时 5s 轮询 + 页面重新可见/重新聚焦时立即补一轮 —— 无事件通道时的兜底
 *      （跨节点事件到不了、目录监听可能漏一次性写入）；
 *   3. 顶栏手动刷新 —— 前两条都错过时由人兜底，绕过缓存直接取磁盘现状。
 *
 * 一次成功的变化顺带刷新文件所在目录：文件大小/是否存在跟着变。
 * 轮询只在「文件视图 + 没有选中会话」时发请求 —— 看不见的东西不刷新。
 */
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import type { FilePayload } from "../services/file";
import type { SessionItem } from "./appSession";
import { dirnameOfPath, type MainViewMode } from "./appPath";

/** 5s 是「人感觉不到」的上限；再快只是烧请求，WS 那条路已经覆盖实时。 */
const FILE_POLL_INTERVAL_MS = 5000;

export type UseFileAutoRefreshParams = {
  fileRef: RefObject<FilePayload | null>;
  mainViewRef: RefObject<MainViewMode>;
  selectedSessionRef: RefObject<SessionItem | null>;
  refreshCurrentFileContent: (
    rootID: string,
    changedPath: string,
    options?: { force?: boolean },
  ) => Promise<boolean>;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void> | void;
};

export function useFileAutoRefresh({
  fileRef,
  mainViewRef,
  selectedSessionRef,
  refreshCurrentFileContent,
  refreshTreeDir,
}: UseFileAutoRefreshParams) {
  const [reloadingFile, setReloadingFile] = useState(false);
  const reloadingRef = useRef(false);

  const syncCurrentFile = useCallback(
    async (force: boolean): Promise<boolean> => {
      const current = fileRef.current;
      if (!current?.root || !current.path) return false;
      if (mainViewRef.current !== "files" || selectedSessionRef.current) return false;
      const changed = await refreshCurrentFileContent(
        current.root,
        current.path,
        force ? { force: true } : undefined,
      );
      if (changed) {
        void refreshTreeDir(current.root, dirnameOfPath(current.path), false);
      }
      return changed;
    },
    [fileRef, mainViewRef, refreshCurrentFileContent, refreshTreeDir, selectedSessionRef],
  );

  /** 顶栏按钮：返回 false 表示没刷成功（视图不对、文件在读/编辑、或请求失败）。 */
  const reloadCurrentFile = useCallback(async (): Promise<boolean> => {
    if (reloadingRef.current) return false;
    reloadingRef.current = true;
    setReloadingFile(true);
    try {
      return await syncCurrentFile(true);
    } finally {
      reloadingRef.current = false;
      setReloadingFile(false);
    }
  }, [syncCurrentFile]);

  useEffect(() => {
    let stopped = false;
    const tick = () => {
      if (stopped) return;
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      void syncCurrentFile(false);
    };
    const onVisible = () => {
      if (typeof document !== "undefined" && document.visibilityState === "visible") tick();
    };
    tick();
    const timer = window.setInterval(tick, FILE_POLL_INTERVAL_MS);
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("pageshow", onVisible);
    window.addEventListener("focus", onVisible);
    return () => {
      stopped = true;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("pageshow", onVisible);
      window.removeEventListener("focus", onVisible);
    };
  }, [syncCurrentFile]);

  return { onReloadFile: reloadCurrentFile, reloadingFile };
}

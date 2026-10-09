import { useEffect } from "react";
import { inferReadModeFromPlugin } from "./appMisc";
import type { ViewerSelection } from "./appSession";
import type { FilePayload } from "../services/file";
import type { ActionHandlers } from "./useActionHandlers";
import type { ViewPlugin } from "../plugins/manager";
import type { useAppPluginRender } from "./useAppPluginRender";

/**
 * 插件渲染完成后要做的三件事：光标落位、阅读模式推断、滚动位置跟随章节。
 */
export type UseAppPluginRenderEffectsParams = {
  file: FilePayload | null;
  currentFileEditing: boolean;
  pluginBypass: boolean;
  matchedPlugin: ViewPlugin | null;
  pluginRender: ReturnType<typeof useAppPluginRender>["pluginRender"];
  pluginQuery: Record<string, string>;
  currentRootId: string | null;
  actionHandlers: ActionHandlers;
  fileCursorRef: React.RefObject<number>;
  handleViewerSelectionChange: (next: ViewerSelection | null) => void;
  pluginContentRef: React.RefObject<HTMLDivElement | null>;
  lastPluginChapterRef: React.RefObject<string>;
  fullUpgradeAttemptRef: React.RefObject<string>;
};

/**
 * 三条都依赖 `pluginRender?.output`，所以都排在渲染之后 —— 这正是
 * 调用点必须在 `useAppPluginRender` 解构之后的原因（它给出 pluginRender 本身）。
 *
 * 两条 effect 的前几行长得几乎一样（都是 `if (!file || pluginBypass || !pluginRender?.output)`
 * 守卫后 return），**但依赖数组不同**：一条跟 `handleViewerSelectionChange`，
 * 一条跟 `pluginQuery.chapter`。合并成一条会让「换章节」和「选中项变化」互相触发。
 *
 * `inferReadModeFromPlugin` 是纯函数，用来从插件输出回推阅读模式，别把它的结果缓存 ——
 * 插件可能对同一份内容给出不同模式（取决于查询参数）。
 */
export function useAppPluginRenderEffects(params: UseAppPluginRenderEffectsParams) {
  const { file, currentFileEditing, pluginBypass, matchedPlugin, pluginRender, pluginQuery, currentRootId, actionHandlers, fileCursorRef, handleViewerSelectionChange, pluginContentRef, lastPluginChapterRef, fullUpgradeAttemptRef } = params;

  useEffect(() => {
    if (!file || currentFileEditing || pluginBypass || !matchedPlugin) return;
    if (inferReadModeFromPlugin(matchedPlugin) !== "full") return;
    if (!file.truncated) return;
    const root = file.root || currentRootId;
    if (!root) return;
    const upgradeKey = `${root}:${file.path}:${matchedPlugin.name}:${JSON.stringify(pluginQuery)}`;
    if (fullUpgradeAttemptRef.current === upgradeKey) return;
    fullUpgradeAttemptRef.current = upgradeKey;
    void actionHandlers.open({
      path: file.path,
      root,
      cursor: fileCursorRef.current || 0,
      readMode: "full",
      preservePluginQuery: true,
    });
  }, [
    file,
    currentFileEditing,
    pluginBypass,
    matchedPlugin,
    currentRootId,
    pluginQuery,
    actionHandlers,
  ]);

  useEffect(() => {
    if (!file || pluginBypass || !pluginRender?.output) {
      return;
    }
    const handleSelectionChange = () => {
      const root = pluginContentRef.current;
      const selection = window.getSelection();
      if (!root || !selection || selection.rangeCount === 0 || selection.isCollapsed) {
        handleViewerSelectionChange(null);
        return;
      }
      const range = selection.getRangeAt(0);
      const commonAncestor = range.commonAncestorContainer;
      if (!root.contains(commonAncestor)) {
        handleViewerSelectionChange(null);
        return;
      }
      const text = selection.toString();
      if (!text.trim()) {
        handleViewerSelectionChange(null);
        return;
      }
      handleViewerSelectionChange({
        filePath: file.path,
        text,
      });
    };
    document.addEventListener("selectionchange", handleSelectionChange);
    return () => {
      document.removeEventListener("selectionchange", handleSelectionChange);
      handleViewerSelectionChange(null);
    };
  }, [file, pluginBypass, pluginRender?.output, handleViewerSelectionChange]);

  useEffect(() => {
    if (!file || pluginBypass || !pluginRender?.output) {
      lastPluginChapterRef.current = "";
      return;
    }
    const chapterKey = `${file.path}:${pluginQuery.chapter || "1"}`;
    if (!lastPluginChapterRef.current) {
      lastPluginChapterRef.current = chapterKey;
      return;
    }
    if (lastPluginChapterRef.current === chapterKey) {
      return;
    }
    lastPluginChapterRef.current = chapterKey;
    requestAnimationFrame(() => {
      pluginContentRef.current?.scrollTo({ top: 0, behavior: "auto" });
    });
  }, [file, pluginBypass, pluginRender?.output, pluginQuery.chapter]);

}

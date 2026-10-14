import { useCallback } from "react";
import { readURLState } from "./appPath";
import { pushViewHistoryEntry } from "../services/platform/backNavigation";
import type { MainViewMode, URLState } from "./appPath";
import type { ActionHandlers } from "./useActionHandlers";
import type { FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload } from "../services/git";

/**
 * 主视图的切换动作：左下角四态切换器、以及在文件视图里「回到原始文件 / 回到插件视图」。
 */
export type UseAppMainViewActionsParams = {
  currentRootIdRef: React.RefObject<string | null>;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  interactionModeRef: React.RefObject<"main" | "drawer">;
  setInteractionMode: React.Dispatch<React.SetStateAction<"main" | "drawer">>;
  mainViewRef: React.RefObject<MainViewMode>;
  switchMainView: (mode: MainViewMode) => void;
  file: FilePayload | null;
  gitDiff: GitDiffPayload | null;
  mainEntriesRef: React.RefObject<FileEntry[]>;
  mainDirectoryErrorRef: React.RefObject<string>;
  selectedDirRef: React.RefObject<string | null>;
  actionHandlersRef: React.RefObject<ActionHandlers>;
  actionHandlers: ActionHandlers;
  replaceURLState: (next: URLState) => void;
  pluginBypassRef: React.RefObject<boolean>;
  setPluginBypass: React.Dispatch<React.SetStateAction<boolean>>;
  fileCursorRef: React.RefObject<number>;
};

/**
 * handleMainViewSwitcherChange 是四态切换器的入口。三件事按顺序做，顺序有意义：
 * 先关该项目的抽屉、把交互模式拉回 main（否则切面板后悬浮框还浮着），
 * 再 pushViewHistoryEntry 记下**切之前**的视图（不记的话侧滑/后退无路可退），
 * 最后才 switchMainView。
 *
 * 切到 files 时有个补目录的分支：从没点过任何目录的话主区是空的，补上项目顶层。
 * 三个否条件都是有意的 —— 已经有条目或报错态说明有内容，别动；
 * 开着 file/gitDiff 时更不能补，因为 open_dir 会 setFile(null)，
 * 把刚看的文件抹掉（这正是「切回文件丢内容」那个 bug）。
 *
 * 末尾只改 URL 的 view 字段，root/node/session/pluginQuery 原样保留；
 * 离开文件态时把 file 摘掉，否则会写出「view=board 却还带着 file」的自相矛盾状态。
 *
 * switchToRawFileView / switchToPluginView 只差 pluginBypass 一个布尔：
 * 两者都复用 actionHandlers.open，保留 cursor 与插件查询。
 */
export function useAppMainViewActions(params: UseAppMainViewActionsParams) {
  const { currentRootIdRef, setDrawerOpenForRoot, interactionModeRef, setInteractionMode, mainViewRef, switchMainView, file, gitDiff, mainEntriesRef, mainDirectoryErrorRef, selectedDirRef, actionHandlersRef, actionHandlers, replaceURLState, pluginBypassRef, setPluginBypass, fileCursorRef } = params;

  const handleMainViewSwitcherChange = useCallback((mode: MainViewMode) => {
    const rootID = currentRootIdRef.current;
    if (rootID) setDrawerOpenForRoot(rootID, false);
    interactionModeRef.current = "main";
    setInteractionMode("main");
    // 先落一条历史（存切之前的视图），再切。没有这条浏览器历史就永远只有一项，
    // 侧滑/后退无路可退 —— 这正是「侧滑直接退出」的直接原因。
    pushViewHistoryEntry(mainViewRef.current);
    switchMainView(mode);
    // 切到文件面板但从没点过任何目录时，主区是空的：把当前项目顶层目录补上。
    // 已经有内容（含报错态）就别动，用户点过的目录优先于这个默认值。
    // 开着文件/diff 时也不能补：open_dir 会 setFile(null)，补目录等于把用户
    // 刚看的文件抹掉 —— 那正是「切回文件丢内容」的第二个根因。
    if (mode === "files" && rootID && !file && !gitDiff && !mainEntriesRef.current.length && !mainDirectoryErrorRef.current) {
      const currentDir = selectedDirRef.current || "";
      // 尚停留在上一个项目时（selectedDir 属于别的 root），直接落在新项目根目录
      if (!currentDir || currentDir === "." || currentDir === rootID || currentDir.startsWith(`${rootID}/`)) {
        void actionHandlersRef.current.open_dir({
          path: rootID,
          root: rootID,
          isRoot: true,
          forceDirectory: true,
        });
      }
    }
    // 只改 URL 的 view，保留 root/node/session/pluginQuery。
    // 离开文件态时把 file 摘掉，否则会写出「view=board 且 file=…」的自相矛盾深链。
    const current = readURLState();
    replaceURLState({
      ...current,
      root: current.root || rootID || "",
      file: mode === "files" ? current.file : "",
      view: mode,
    });
  }, [file, gitDiff, replaceURLState, setDrawerOpenForRoot, switchMainView]);

  const switchToRawFileView = useCallback(async () => {
    if (!file) return;
    const root = file.root || currentRootIdRef.current;
    if (!root) return;
    pluginBypassRef.current = true;
    setPluginBypass(true);
    await actionHandlers.open({
      path: file.path,
      root,
      cursor: fileCursorRef.current || 0,
      readMode: "incremental",
      preservePluginQuery: true,
    });
  }, [file, actionHandlers]);

  const switchToPluginView = useCallback(async () => {
    if (!file) return;
    const root = file.root || currentRootIdRef.current;
    if (!root) return;
    pluginBypassRef.current = false;
    setPluginBypass(false);
    await actionHandlers.open({
      path: file.path,
      root,
      cursor: fileCursorRef.current || 0,
      preservePluginQuery: true,
    });
  }, [file, actionHandlers]);

  return {
    handleMainViewSwitcherChange,
    switchToRawFileView,
    switchToPluginView,
  };
}

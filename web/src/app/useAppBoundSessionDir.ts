import { useCallback } from "react";
import type { ActionHandlers } from "./useActionHandlers";
import type { MainViewMode } from "./appPath";
import type { FileEntry } from "../services/prefs/directorySort";

/**
 * 切到某个项目时「优先恢复它绑定的会话、否则落到目录」这条路径。
 */
export type UseAppBoundSessionDirParams = {
  actionHandlersRef: React.RefObject<ActionHandlers>;
  loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => Promise<void>;
  mainDirectoryErrorRef: React.RefObject<string>;
  mainEntriesRef: React.RefObject<FileEntry[]>;
  mainViewRef: React.RefObject<MainViewMode>;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
  tryShowBoundSessionForRoot: (rootID: string | null | undefined, options?: { pluginQuery?: Record<string, string>; closeLeftSidebar?: boolean; }) => Promise<boolean>;
};

/**
 * 这是「切项目」的统一入口：先试 tryShowBoundSessionForRoot 恢复该项目上次绑定的会话
 * （带插件查询等参数）；没恢复成功就 open_dir 落到项目根目录，到此为止。
 *
 * 恢复成功时还有两件收尾：loadSessionsOnRestore 为真就强制重拉一次该项目的会话列表；
 * 如果当前停在文件视图、而主区既没有条目也没有错误（=一片空白），就把项目顶层目录补上
 * —— 空白的文件视图看起来像「项目加载失败」，补上目录至少能看见东西。
 */
export function useAppBoundSessionDir(params: UseAppBoundSessionDirParams) {
  const { actionHandlersRef, loadSessionsForRoot, mainDirectoryErrorRef, mainEntriesRef, mainViewRef, refreshTreeDir, tryShowBoundSessionForRoot } = params;

  const showBoundSessionOrRootDir = useCallback(
    async (
      rootID: string,
      options?: {
        pluginQuery?: Record<string, string>;
        closeLeftSidebar?: boolean;
        loadSessionsOnRestore?: boolean;
        suppressTreeExpand?: boolean;
      },
    ) => {
      const restored = await tryShowBoundSessionForRoot(rootID, {
        pluginQuery: options?.pluginQuery,
        closeLeftSidebar: options?.closeLeftSidebar,
      });
      if (!restored) {
        await actionHandlersRef.current.open_dir({
          path: rootID,
          root: rootID,
          isRoot: true,
          forceDirectory: true,
          suppressTreeExpand: options?.suppressTreeExpand,
        });
        return;
      }
      if (options?.loadSessionsOnRestore) {
        void loadSessionsForRoot(rootID, { replace: true, force: true });
      }
      // 文件态没有内容可显示：把当前项目顶层目录补上，行为与手动切面板一致
      if (
        mainViewRef.current === "files" &&
        !mainEntriesRef.current.length &&
        !mainDirectoryErrorRef.current
      ) {
        await actionHandlersRef.current.open_dir({
          path: rootID,
          root: rootID,
          isRoot: true,
          forceDirectory: true,
          suppressTreeExpand: true,
        });
        return;
      }
      await refreshTreeDir(rootID, ".", false);
    },
    [loadSessionsForRoot, refreshTreeDir, tryShowBoundSessionForRoot],
  );

  return {
    showBoundSessionOrRootDir,
  };
}

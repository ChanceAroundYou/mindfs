import { useCallback } from "react";
import { sessionService } from "../services/session";
import { TASK_TEMPLATE_ALL_FILTER } from "./appStorage";
import type { ActionHandlers } from "./useActionHandlers";
import type { MainViewMode } from "./appPath";

/**
 * 工作台里的两个动作：回答 agent 的提问、从工作台打开某个项目。
 */
export type UseAppWorkspaceActionsParams = {
  actionHandlersRef: React.RefObject<ActionHandlers>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  switchMainView: (mode: MainViewMode) => void;
  setTaskTemplateFilter: React.Dispatch<React.SetStateAction<string>>;
};

/**
 * handleAskUserAnswer 是看板/工作台面板上「等待你」那类提问的回传口，
 * 只做转发不做状态更新 —— 会话状态由 WS 回包驱动。
 *
 * openWorkspaceProject 里点项目 = 切到该项目并落到「项目看板」：先走
 * open_dir 把项目树/文件视图切过去（preservePluginQuery 保住插件查询），
 * 再把主视图切到 board、清掉模板筛选。
 */
export function useAppWorkspaceActions(params: UseAppWorkspaceActionsParams) {
  const { actionHandlersRef, getNodeIdForRoot, switchMainView, setTaskTemplateFilter } = params;

  const handleAskUserAnswer = useCallback(
    async (input: {
      rootId: string;
      sessionKey: string;
      agent?: string;
      toolUseId: string;
      answers: Record<string, string>;
    }) => {
      await sessionService.answerQuestion(
        input.rootId,
        input.sessionKey,
        input.agent,
        input.toolUseId,
        input.answers,
      );
    },
    [],
  );

  const openWorkspaceProject = useCallback(async (rootId: string) => {
    if (!rootId) return;
    // R6：工作台里点项目 = 切到该项目并落到「项目看板」，而不是回到文件列表
    await actionHandlersRef.current.open_dir({
      path: rootId,
      root: rootId,
      isRoot: true,
      nodeId: getNodeIdForRoot(rootId) || undefined,
      preservePluginQuery: true,
    });
    switchMainView("board");
    setTaskTemplateFilter(TASK_TEMPLATE_ALL_FILTER);
  }, [setTaskTemplateFilter, switchMainView]);

  return {
    handleAskUserAnswer,
    openWorkspaceProject,
  };
}

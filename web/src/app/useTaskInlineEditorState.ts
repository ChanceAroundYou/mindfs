import { useEffect } from "react";
import type { Dispatch, SetStateAction } from "react";
import { fetchCandidates, type CandidateItem } from "../services/candidates";
import { CANDIDATE_FETCH_DEBOUNCE_MS } from "./appStorage";
import { useTaskInlineCandidates } from "./useTaskInlineCandidates";
import type { TaskInlineEditState } from "./appTask";

type UseTaskInlineEditorStateParams = {
  currentRootId: string | null;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  taskInlineEdit: TaskInlineEditState | null;
  taskInlineActiveToken: { type: "file" | "slash" | "prompt" | "command"; query: string } | null;
  setTaskInlineCandidateIndex: Dispatch<SetStateAction<number>>;
  setTaskInlineCandidates: Dispatch<SetStateAction<CandidateItem[]>>;
};

/**
 * 新建任务面板的 @文件 / /命令 候选。
 *
 * 原先和任务模板状态挤在同一个 hook 里；模板下线（Phase 3）后模板部分删除，
 * 候选逻辑单独留在这里。编辑器的文本灌入与聚焦在 TaskInlineEditPanel 内，
 * 因为它需要知道面板何时打开。
 */
export function useTaskInlineEditorState({
  currentRootId,
  getNodeIdForRoot,
  taskInlineEdit,
  taskInlineActiveToken,
  setTaskInlineCandidateIndex,
  setTaskInlineCandidates,
}: UseTaskInlineEditorStateParams) {
  useTaskInlineCandidates({
    AbortController,
    CANDIDATE_FETCH_DEBOUNCE_MS,
    currentRootId,
    fetchCandidates,
    getNodeIdForRoot,
    setTaskInlineCandidateIndex,
    setTaskInlineCandidates,
    taskInlineActiveToken,
    taskInlineEdit,
    useEffect,
  });
}

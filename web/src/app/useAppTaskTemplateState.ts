import { useEffect } from "react";
import type { Dispatch, SetStateAction } from "react";
import { fetchCandidates, type CandidateItem } from "../services/candidates";
import { CANDIDATE_FETCH_DEBOUNCE_MS } from "./appStorage";
import { firstAgentStage } from "./appTask";
import { useTaskInlineCandidates } from "./useTaskInlineCandidates";
import { useTaskTemplates } from "./useTaskTemplates";
import type { TaskInlineEditState } from "./appTask";
import type { TokenEditorHandle } from "../components/editor/TokenEditor";

type Ref<T> = { current: T };

type UseAppTaskTemplateStateParams = {
  currentRootId: string | null;
  scopedRootKey: (rootId: string) => string;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  taskInlineEdit: TaskInlineEditState | null;
  taskInlineEditorRef: Ref<TokenEditorHandle | null>;
  taskInlineActiveToken: { type: "file" | "slash" | "prompt" | "command"; query: string } | null;
  setTaskInlineCandidateIndex: Dispatch<SetStateAction<number>>;
  setTaskInlineCandidates: Dispatch<SetStateAction<CandidateItem[]>>;
};

export function useAppTaskTemplateState({
  currentRootId,
  scopedRootKey,
  getNodeIdForRoot,
  taskInlineEdit,
  taskInlineEditorRef,
  taskInlineActiveToken,
  setTaskInlineCandidateIndex,
  setTaskInlineCandidates,
}: UseAppTaskTemplateStateParams) {
  const taskTemplateState = useTaskTemplates({ currentRootId, scopedRootKey });

  useEffect(() => {
    if (!taskInlineEdit) return;
    window.setTimeout(() => {
      taskInlineEditorRef.current?.setText(taskInlineEdit.text || "");
      // TokenEditor 的 setText 不再抢焦点，开面板时由这里主动聚焦。
      taskInlineEditorRef.current?.focus();
    }, 0);
  }, [taskInlineEdit?.templateId]);

  useTaskInlineCandidates({
    AbortController,
    CANDIDATE_FETCH_DEBOUNCE_MS,
    currentRootId,
    fetchCandidates,
    firstAgentStage,
    getNodeIdForRoot,
    setTaskInlineCandidateIndex,
    setTaskInlineCandidates,
    taskInlineActiveToken,
    taskInlineEdit,
    taskTemplateFilter: taskTemplateState.taskTemplateFilter,
    taskTemplates: taskTemplateState.taskTemplates,
    useEffect,
  });

  return taskTemplateState;
}

// 从 App.tsx 抽出的 useEffect（机械搬移，effect 体逐字未改）。
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { fetchCandidates, type CandidateItem } from "../services/candidates";
import { CANDIDATE_FETCH_DEBOUNCE_MS, DIRECTORY_SORT_OVERRIDES_STORAGE_KEY, GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY, GIT_HISTORY_EXPANDED_STORAGE_KEY, GIT_STATUS_EXPANDED_STORAGE_KEY, LAST_ROOT_NODE_STORAGE_KEY, LAST_ROOT_STORAGE_KEY, loadWorkspaceCollapsed, loadWorkspaceFilter, MOBILE_ENTER_KEY_SEND_STORAGE_KEY, PLUGIN_QUERY_STORAGE_PREFIX, saveWorkspaceCollapsed, saveWorkspaceFilter, SIDEBARS_SWAPPED_STORAGE_KEY, TASK_TEMPLATE_ALL_FILTER, TASK_TEMPLATE_SELECTION_STORAGE_KEY, TREE_SORT_STORAGE_KEY, type WorkspaceBoardFilter } from "./appStorage";
import { applyStageOverride, currentTaskInputFromDetail, DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, firstAgentStage, firstTaskInputFromDetail, firstUserInputTemplate, isTerminalKanbanTask, latestTaskStageRun, normalizeFastService, parseTaskSessionErrorDetails, parseTaskSessionErrorMessage, previousTaskInputsFromDetail, taskSessionKeysFromDetail, taskStagesForCreate, taskStatusLabel } from "./appTask";
import type { Dispatch, SetStateAction } from "react";
import {
  beginTaskFinishWorktree,
  createTask,
  deleteTask,
  deleteTaskTemplate,
  // FinishWorktreeConflict 是 class 不是 type：下面要用 instanceof 分流
  // （冲突要列文件清单，其它失败只报一句）。
  FinishWorktreeConflict,
  fetchTaskDetails,
  fetchTaskTemplates,
  getCachedTaskDetails,
  getCachedTaskMeta,
  moveTask,
  pruneCachedTaskDetails,
  rebuildTaskWorktree,
  saveTaskTemplate,
  upsertCachedTaskDetails,
  type KanbanTask,
  type StageRun,
  type TaskDetail,
  type TaskOverviewItem,
  type StageTemplate,
  type TaskTemplate,
} from "../services/tasks";
import { TaskInlineEditState } from "./appTask";

export interface useTaskInlineCandidatesParams {
  AbortController: any;
  CANDIDATE_FETCH_DEBOUNCE_MS: typeof CANDIDATE_FETCH_DEBOUNCE_MS;
  currentRootId: string | null;
  fetchCandidates: typeof fetchCandidates;
  firstAgentStage: typeof firstAgentStage;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  setTaskInlineCandidateIndex: Dispatch<SetStateAction<number>>;
  setTaskInlineCandidates: Dispatch<SetStateAction<CandidateItem[]>>;
  taskInlineActiveToken: { type: "file" | "slash" | "prompt" | "command"; query: string; } | null;
  taskInlineEdit: TaskInlineEditState | null;
  taskTemplateFilter: string;
  taskTemplates: TaskTemplate[];
  useEffect: typeof useEffect;
}

export function useTaskInlineCandidates(params: useTaskInlineCandidatesParams) {
  const AbortController = params.AbortController;
  const CANDIDATE_FETCH_DEBOUNCE_MS = params.CANDIDATE_FETCH_DEBOUNCE_MS;
  const currentRootId = params.currentRootId;
  const fetchCandidates = params.fetchCandidates;
  const firstAgentStage = params.firstAgentStage;
  const getNodeIdForRoot = params.getNodeIdForRoot;
  const setTaskInlineCandidateIndex = params.setTaskInlineCandidateIndex;
  const setTaskInlineCandidates = params.setTaskInlineCandidates;
  const taskInlineActiveToken = params.taskInlineActiveToken;
  const taskInlineEdit = params.taskInlineEdit;
  const taskTemplateFilter = params.taskTemplateFilter;
  const taskTemplates = params.taskTemplates;
  const useEffect = params.useEffect;

useEffect(() => {
    // 候选跟着面板的目标项目走，不跟着「当前选中项目」走 —— 工作台发起时两者不是一个。
    const tokenRootId = taskInlineEdit?.targetRootId || currentRootId;
    if (!taskInlineActiveToken || !tokenRootId) {
      setTaskInlineCandidates([]);
      setTaskInlineCandidateIndex(0);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      const selectedTemplate = taskTemplates.find((template) => template.id === taskTemplateFilter) || null;
      const agent = firstAgentStage(selectedTemplate)?.agent || "";
      fetchCandidates({
        rootId: tokenRootId,
        type: taskInlineActiveToken.type === "file"
          ? "file"
          : taskInlineActiveToken.type === "prompt"
            ? "prompt"
            : taskInlineActiveToken.type === "command"
              ? "command"
              : "skill",
        query: taskInlineActiveToken.query,
        agent: taskInlineActiveToken.type === "slash" ? agent : undefined,
        signal: controller.signal,
        nodeId: getNodeIdForRoot(tokenRootId),
      })
        .then((items) => {
          setTaskInlineCandidates(items);
          setTaskInlineCandidateIndex(0);
        })
        .catch((err) => {
          if (controller.signal.aborted) return;
          console.error("Failed to fetch task candidates:", err);
          setTaskInlineCandidates([]);
          setTaskInlineCandidateIndex(0);
        });
    }, CANDIDATE_FETCH_DEBOUNCE_MS);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [currentRootId, taskInlineEdit?.targetRootId, taskInlineActiveToken, taskTemplateFilter, taskTemplates])
}

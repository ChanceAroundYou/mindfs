import { useEffect, useRef, useState } from "react";
import type { RelatedFile } from "../services/session";
import type { CandidateItem } from "../services/candidates";
import type { UploadProgress } from "../services/upload";
import type { TokenEditorHandle } from "../components/editor/TokenEditor";
import type { TaskInlineEditState } from "../app/appTask";
import type { KanbanTask, TaskDetail } from "../services/tasks";
import type { GitBranchesPayload } from "../services/git/types";

export function useTaskWorkspaceState() {
  const [kanbanTasks, setKanbanTasks] = useState<KanbanTask[]>([]);
  const [kanbanTaskCountItems, setKanbanTaskCountItems] = useState<KanbanTask[]>([]);
  const [taskDetailsById, setTaskDetailsById] = useState<Record<string, TaskDetail>>({});
  const [taskFirstInputById, setTaskFirstInputById] = useState<Record<string, string>>({});
  const [taskSessionKeysById, setTaskSessionKeysById] = useState<Record<string, string[]>>({});
  const [taskRelatedFilesById, setTaskRelatedFilesById] = useState<Record<string, RelatedFile[]>>({});
  const taskDetailsByIdRef = useRef<Record<string, TaskDetail>>({});
// 「工作台结构变了，重拉一遍扇出」的动作本体在 useWorkspaceBoard 里（那里才有 refresh），
// 而要按它的调用点在它之前 —— 卡片动作、WS 处理器。所以这里只留一个转发 ref：
// 渲染时把真身挂上去，早期调用点读到 undefined 就跳过（那时工作台根本没打开）。
const workspaceBoardRefreshRef = useRef<(() => void) | null>(null);
  const taskSessionKeysByIdRef = useRef<Record<string, string[]>>({});
  const [selectedKanbanTaskId, setSelectedKanbanTaskId] = useState("");
  const [expandedTaskInputIds, setExpandedTaskInputIds] = useState<Set<string>>(() => new Set());
const [overflowingTaskInputs, setOverflowingTaskInputs] = useState<Set<string>>(() => new Set());
const [collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups] = useState<Set<string>>(() => new Set());
// 看板列折叠（移动端多列换行后空间宝贵，长列默认可收起只留表头）。
const [collapsedKanbanColumns, setCollapsedKanbanColumns] = useState<Set<string>>(() => new Set());
const [taskInlineEdit, setTaskInlineEdit] = useState<TaskInlineEditState | null>(null);
const [taskSessionErrorDialog, setTaskSessionErrorDialog] = useState<{ title: string; message: string; details: string[] } | null>(null);
const [taskInlineActiveToken, setTaskInlineActiveToken] = useState<{ type: "file" | "slash" | "prompt" | "command"; query: string } | null>(null);
const [taskInlineCandidates, setTaskInlineCandidates] = useState<CandidateItem[]>([]);
const [taskInlineCandidateIndex, setTaskInlineCandidateIndex] = useState(0);
const [taskInlineSaving, setTaskInlineSaving] = useState(false);
// 新建任务面板里「用户输入模板」字段说明的展开态（与模板编辑面板同一套组件）。
const [taskInlineHelpKey, setTaskInlineHelpKey] = useState("");
const [taskInlineUploadProgress, setTaskInlineUploadProgress] = useState<UploadProgress | null>(null);
  const [directoryUploadProgress, setDirectoryUploadProgress] = useState<UploadProgress | null>(null);
  const [taskWorktreeBranches, setTaskWorktreeBranches] = useState<GitBranchesPayload>({ branches: [] });
  const [taskWorktreeBranchesLoading, setTaskWorktreeBranchesLoading] = useState(false);
  const [taskWorktreeBranchError, setTaskWorktreeBranchError] = useState("");
  const [kanbanTasksLoading, setKanbanTasksLoading] = useState(false);
  const kanbanLoadSeqRef = useRef(0);
  const kanbanAbortRef = useRef<AbortController | null>(null);
  // refreshTaskWorktree 来自 useProjectTreeWorktrees（后置初始化），
  // 通过 ref 传给 useTaskBoard 避免 TDZ。
  const refreshTaskWorktreeRef = useRef<((rootId: string, worktreePath: string, force?: boolean, targetNodeId?: string) => Promise<void>) | null>(null);
  // 分支列表请求竞态保护：每次发起新请求时递增，只允许最新请求写回状态。
  const taskWorktreeBranchRequestRef = useRef(0);
  const sessionListLoadSeqRef = useRef(0);
  const sessionListAbortRef = useRef<AbortController | null>(null);
// 列表拉取失败告警冷却：断网/抖动时重拉被多个事件反复触发，避免告警刷屏。
const listLoadErrorAtRef = useRef(0);
const taskInlineEditorRef = useRef<TokenEditorHandle | null>(null);
const taskInlineCandidateItemRefs = useRef<Array<HTMLButtonElement | null>>([]);
const taskInlineAttachmentInputRef = useRef<HTMLInputElement | null>(null);
  const taskInlineUploadAbortRef = useRef<AbortController | null>(null);
  const directoryUploadAbortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    taskDetailsByIdRef.current = taskDetailsById;
  }, [taskDetailsById]);

  useEffect(() => {
    taskSessionKeysByIdRef.current = taskSessionKeysById;
  }, [taskSessionKeysById]);
const knownTaskWorktreePathsRef = useRef<Set<string>>(new Set());

  return {
    kanbanTasks,
    setKanbanTasks,
    kanbanTaskCountItems,
    setKanbanTaskCountItems,
    taskDetailsById,
    setTaskDetailsById,
    taskFirstInputById,
    setTaskFirstInputById,
    taskSessionKeysById,
    setTaskSessionKeysById,
    taskRelatedFilesById,
    setTaskRelatedFilesById,
    taskDetailsByIdRef,
    workspaceBoardRefreshRef,
    taskSessionKeysByIdRef,
    selectedKanbanTaskId,
    setSelectedKanbanTaskId,
    expandedTaskInputIds,
    setExpandedTaskInputIds,
    overflowingTaskInputs,
    setOverflowingTaskInputs,
    collapsedTaskCompletionGroups,
    setCollapsedTaskCompletionGroups,
    collapsedKanbanColumns,
    setCollapsedKanbanColumns,
    taskInlineEdit,
    setTaskInlineEdit,
    taskSessionErrorDialog,
    setTaskSessionErrorDialog,
    taskInlineActiveToken,
    setTaskInlineActiveToken,
    taskInlineCandidates,
    setTaskInlineCandidates,
    taskInlineCandidateIndex,
    setTaskInlineCandidateIndex,
    taskInlineSaving,
    setTaskInlineSaving,
    taskInlineHelpKey,
    setTaskInlineHelpKey,
    taskInlineUploadProgress,
    setTaskInlineUploadProgress,
    directoryUploadProgress,
    setDirectoryUploadProgress,
    taskWorktreeBranches,
    setTaskWorktreeBranches,
    taskWorktreeBranchesLoading,
    setTaskWorktreeBranchesLoading,
    taskWorktreeBranchError,
    setTaskWorktreeBranchError,
    kanbanTasksLoading,
    setKanbanTasksLoading,
    kanbanLoadSeqRef,
    kanbanAbortRef,
    refreshTaskWorktreeRef,
    taskWorktreeBranchRequestRef,
    sessionListLoadSeqRef,
    sessionListAbortRef,
    listLoadErrorAtRef,
    taskInlineEditorRef,
    taskInlineCandidateItemRefs,
    taskInlineAttachmentInputRef,
    taskInlineUploadAbortRef,
    directoryUploadAbortRef,
    knownTaskWorktreePathsRef,
  };
}

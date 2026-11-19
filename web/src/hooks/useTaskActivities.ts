// 对话式任务活动流的逻辑层：REST 拉取 + WebSocket 增量 + 断线重连补偿。
//
// 为什么订阅全局 dispatcher 而不是自开 WebSocket：连接由 SessionConnectionManager
// 按当前 root 统一管理，面板只是观察者。`ws.reconnected` 是它的既有事件；断线期间
// 错过的 `task.activity.updated` 靠这一次整表重拉补齐（Review Focus #4）。
import { useCallback, useEffect, useRef, useState } from "react";
import type { SessionServiceEvent } from "../shared/sessionTypes";
import type { HumanGateDecision, TaskActivity, WorkflowChoice } from "../shared/taskComments";
import {
  activityBelongsToTask,
  applyActivityCreated,
  applyActivityUpdated,
  parseTaskActivityPayload,
} from "../shared/activityModel";
import { createActivity, listActivities } from "../services/activities";
import {
  fileTokenPath,
  formatFileToken,
  isUploadAbortError,
  uploadFiles,
} from "../services/upload";
import type { ComposerAttachment, UploadProgress } from "../shared/uploadTypes";
import { runTaskNow } from "../services/task/crud";
import { postTaskHumanGate } from "../services/task/humanGate";
import { sessionService } from "../services/session";

export type UseTaskActivitiesResult = {
  activities: TaskActivity[];
  loading: boolean;
  error: string | null;
  reload: () => Promise<void>;
  /**
   * 发送留言。带附件时先上传，再把 `[file: ...]` token 拼进正文 —— 与新建任务
   * 面板的发送链路一致（附件不占后端字段，仍是纯文本留言）。
   */
  send: (
    body: string,
    workflowChoice: WorkflowChoice,
    workflowVersionId?: string,
    attachments?: ComposerAttachment[],
  ) => Promise<void>;
  sending: boolean;
  sendError: string | null;
  /** 附件上传进度；上传结束（成功/失败/取消）后清空。 */
  uploadProgress: UploadProgress | null;
  cancelUpload: () => void;
  answerQuestion: (activity: TaskActivity, answers: Record<string, string>) => Promise<void>;
  decideGate: (activity: TaskActivity, decision: HumanGateDecision, note?: string) => Promise<void>;
  /** 派发排队中的首条留言（新建任务时勾掉了「立即执行」）。 */
  runQueued: () => Promise<void>;
  runningQueued: boolean;
  promptDeciding: boolean;
  promptError: string | null;
};

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function useTaskActivities(
  taskId: string,
  rootId: string,
  nodeId?: string,
): UseTaskActivitiesResult {
  const [activities, setActivities] = useState<TaskActivity[]>([]);
  const [loading, setLoading] = useState(Boolean(taskId && rootId));
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(null);
  const [runningQueued, setRunningQueued] = useState(false);
  const [promptDeciding, setPromptDeciding] = useState(false);
  const [promptError, setPromptError] = useState<string | null>(null);
  const mountedRef = useRef(true);
  const uploadAbortRef = useRef<AbortController | null>(null);

  const cancelUpload = useCallback(() => {
    uploadAbortRef.current?.abort();
    uploadAbortRef.current = null;
    setUploadProgress(null);
  }, []);

  const load = useCallback(async () => {
    if (!taskId || !rootId) {
      if (mountedRef.current) {
        setActivities([]);
        setLoading(false);
        setError(null);
      }
      return;
    }
    try {
      if (mountedRef.current) setLoading(true);
      const data = await listActivities(taskId, rootId, nodeId);
      if (!mountedRef.current) return;
      setActivities(data);
      setError(null);
    } catch (err) {
      if (mountedRef.current) setError(errorMessage(err));
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, [taskId, rootId, nodeId]);

  useEffect(() => {
    mountedRef.current = true;
    void load();
    return () => {
      mountedRef.current = false;
    };
  }, [load]);

  useEffect(() => {
    const unsubscribe = sessionService.dispatcher.subscribeEvents((event: SessionServiceEvent) => {
      // 重连补偿：断线期间的更新没有排队，整表重拉是唯一可靠的对齐方式。
      if (event.type === "ws.reconnected") {
        void load();
        return;
      }
      const payload = (event.payload || {}) as {
        task_id?: unknown;
        root_id?: unknown;
        activity?: TaskActivity;
        activity_id?: string;
        changes?: Record<string, unknown>;
      };
      if (!activityBelongsToTask(payload, taskId, rootId)) return;
      if (event.type === "task.activity.created") {
        setActivities((prev) => applyActivityCreated(prev, payload.activity));
      } else if (event.type === "task.activity.updated") {
        setActivities((prev) => applyActivityUpdated(prev, payload.activity_id, payload.changes));
      }
    });
    return unsubscribe;
  }, [taskId, rootId, load]);

  const send = useCallback(
    async (
      body: string,
      workflowChoice: WorkflowChoice,
      workflowVersionId?: string,
      attachments: ComposerAttachment[] = [],
    ) => {
      try {
        setSending(true);
        setSendError(null);

        let attachmentTokens = "";
        if (attachments.length > 0) {
          const controller = new AbortController();
          uploadAbortRef.current = controller;
          try {
            const uploaded = await uploadFiles({
              rootId,
              files: attachments.map((attachment) => attachment.file),
              onProgress: setUploadProgress,
              signal: controller.signal,
              nodeId,
            });
            attachmentTokens = uploaded.map((file) => formatFileToken(fileTokenPath(file))).join("\n");
          } finally {
            uploadAbortRef.current = null;
            setUploadProgress(null);
          }
        }

        const payload = [body.trim(), attachmentTokens].filter(Boolean).join("\n");
        const result = await createActivity(
          taskId,
          rootId,
          {
            body: payload,
            workflow_choice: workflowChoice,
            workflow_version_id: workflowVersionId,
          },
          nodeId,
        );
        // 乐观并入 HTTP 响应的两条活动；WS 回声同 id 会被 applyActivityCreated 覆盖，
        // 不会出现重复卡片，也不依赖广播先到。
        setActivities((prev) => {
          let next = applyActivityCreated(prev, result.user_activity);
          if (result.agent_activity) next = applyActivityCreated(next, result.agent_activity);
          return next;
        });
      } catch (err) {
        // 用户主动取消上传不是错误：保留草稿，不写红色错误条。
        if (!isUploadAbortError(err)) setSendError(errorMessage(err));
        throw err;
      } finally {
        setSending(false);
      }
    },
    [taskId, rootId, nodeId],
  );

  const answerQuestion = useCallback(
    async (activity: TaskActivity, answers: Record<string, string>) => {
      const payload = parseTaskActivityPayload(activity);
      const targetRoot = activity.root_id || rootId;
      const sessionKey = activity.session_key || "";
      const toolUseId =
        `${activity.tool_use_id || payload.tool_use_id || ""}`.trim();
      if (!targetRoot || !sessionKey || !toolUseId) {
        setPromptError(errorMessage(new Error("question card is missing its session or tool id")));
        throw new Error("question card is missing its session or tool id");
      }
      setPromptDeciding(true);
      setPromptError(null);
      try {
        const answered = await sessionService.answerQuestion(
          targetRoot,
          sessionKey,
          typeof payload.agent === "string" ? payload.agent : undefined,
          toolUseId,
          answers,
        );
        if (!answered) {
          throw new Error("answer was not accepted");
        }
        // 乐观收口：WS 回声到达前先让卡片退出等待态，避免用户重复点击。
        setActivities((prev) =>
          applyActivityUpdated(prev, activity.id, {
            status: "completed",
            payload_json: JSON.stringify({ ...payload, answers }),
          }),
        );
      } catch (err) {
        setPromptError(errorMessage(err));
        throw err;
      } finally {
        setPromptDeciding(false);
      }
    },
    [rootId],
  );

  const decideGate = useCallback(
    async (activity: TaskActivity, decision: HumanGateDecision, note?: string) => {
      const payload = parseTaskActivityPayload(activity);
      const workflowNodeId = `${payload.node_id || ""}`.trim();
      if (!workflowNodeId) {
        setPromptError(errorMessage(new Error("human gate card is missing its node id")));
        throw new Error("human gate card is missing its node id");
      }
      setPromptDeciding(true);
      setPromptError(null);
      try {
        await postTaskHumanGate(
          activity.root_id || rootId,
          taskId,
          { workflowNodeId, decision, note },
          nodeId,
        );
        setActivities((prev) =>
          applyActivityUpdated(prev, activity.id, {
            status: decision === "approve" ? "completed" : "cancelled",
            payload_json: JSON.stringify({ ...payload, decision }),
          }),
        );
      } catch (err) {
        setPromptError(errorMessage(err));
        throw err;
      } finally {
        setPromptDeciding(false);
      }
    },
    [rootId, taskId, nodeId],
  );

  const runQueued = useCallback(async () => {
    setRunningQueued(true);
    setSendError(null);
    try {
      await runTaskNow(rootId, taskId, nodeId);
      // 乐观清掉排队标记；Agent 回复由 HTTP 回包/WS 增量补进活动流。
      setActivities((prev) => prev.map((activity) => (
        activity.status === "queued" ? { ...activity, status: "" } : activity
      )));
    } catch (err) {
      setSendError(errorMessage(err));
      throw err;
    } finally {
      setRunningQueued(false);
    }
  }, [taskId, rootId, nodeId]);

  return {
    activities,
    loading,
    error,
    reload: load,
    send,
    sending,
    sendError,
    uploadProgress,
    cancelUpload,
    answerQuestion,
    decideGate,
    runQueued,
    runningQueued,
    promptDeciding,
    promptError,
  };
}

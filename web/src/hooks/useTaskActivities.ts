// 对话式任务活动流的逻辑层：REST 拉取 + WebSocket 增量 + 断线重连补偿。
//
// 为什么订阅全局 dispatcher 而不是自开 WebSocket：连接由 SessionConnectionManager
// 按当前 root 统一管理，面板只是观察者。`ws.reconnected` 是它的既有事件；断线期间
// 错过的 `task.activity.updated` 靠这一次整表重拉补齐（Review Focus #4）。
import { useCallback, useEffect, useRef, useState } from "react";
import type { SessionServiceEvent } from "../shared/sessionTypes";
import type { TaskActivity, WorkflowChoice } from "../shared/taskComments";
import {
  activityBelongsToTask,
  applyActivityCreated,
  applyActivityUpdated,
} from "../shared/activityModel";
import { createActivity, listActivities } from "../services/activities";
import { sessionService } from "../services/session";

export type UseTaskActivitiesResult = {
  activities: TaskActivity[];
  loading: boolean;
  error: string | null;
  reload: () => Promise<void>;
  send: (body: string, workflowChoice: WorkflowChoice, workflowVersionId?: string) => Promise<void>;
  sending: boolean;
  sendError: string | null;
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
  const mountedRef = useRef(true);

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
    async (body: string, workflowChoice: WorkflowChoice, workflowVersionId?: string) => {
      try {
        setSending(true);
        setSendError(null);
        const result = await createActivity(
          taskId,
          rootId,
          {
            body,
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
        setSendError(errorMessage(err));
        throw err;
      } finally {
        setSending(false);
      }
    },
    [taskId, rootId, nodeId],
  );

  return { activities, loading, error, reload: load, send, sending, sendError };
}
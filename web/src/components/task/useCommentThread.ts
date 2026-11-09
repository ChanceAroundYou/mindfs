import { useCallback, useEffect, useRef, useState } from "react";
import { fetchTaskComments, postTaskComment, type TaskActivity, type WorkflowChoice } from "../../services/task/comments";

export type CommentThreadState = {
  activities: TaskActivity[];
  loading: boolean;
  error: string | null;
  posting: boolean;
  reload: () => Promise<void>;
  post: (body: string, choice?: WorkflowChoice, versionId?: string) => Promise<void>;
};

export function useCommentThread(rootId: string, taskId: string, nodeId?: string): CommentThreadState {
  const [activities, setActivities] = useState<TaskActivity[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [posting, setPosting] = useState(false);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  const reload = useCallback(async () => {
    if (!rootId || !taskId) return;
    setLoading(true);
    setError(null);
    try {
      const items = await fetchTaskComments(rootId, taskId, nodeId);
      if (mountedRef.current) setActivities(items);
    } catch (e) {
      if (mountedRef.current) setError(e instanceof Error ? e.message : "加载留言失败");
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, [rootId, taskId, nodeId]);

  useEffect(() => { void reload(); }, [reload]);

  const post = useCallback(async (body: string, choice: WorkflowChoice = "empty", versionId?: string) => {
    const text = body.trim();
    if (!text) return;
    setPosting(true);
    setError(null);
    try {
      const res = await postTaskComment(rootId, taskId, { body: text, workflowChoice: choice, workflowVersionId: versionId }, nodeId);
      if (mountedRef.current) {
        setActivities((prev) => (prev.some((a) => a.id === res.activity.id) ? prev : [...prev, res.activity]));
      }
    } catch (e) {
      if (mountedRef.current) setError(e instanceof Error ? e.message : "发送失败");
      throw e;
    } finally {
      if (mountedRef.current) setPosting(false);
    }
  }, [rootId, taskId, nodeId]);

  return { activities, loading, error, posting, reload, post };
}

import { useState, useEffect } from 'react';
import { TaskActivity } from '../../shared/taskComments';

interface UseTaskActivitiesResult {
  activities: TaskActivity[];
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  postActivity: (body: string, workflowChoice: string) => Promise<void>;
}

export function useTaskActivities(
  taskId: string | null,
  rootId: string | null
): UseTaskActivitiesResult {
  const [activities, setActivities] = useState<TaskActivity[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchActivities = async () => {
    if (!taskId || !rootId) {
      setActivities([]);
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const response = await fetch(
        `/api/tasks/${taskId}/activities?root=${encodeURIComponent(rootId)}`
      );

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      const data = await response.json();
      setActivities(data.activities || []);
    } catch (err) {
      console.error('Failed to fetch activities:', err);
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  };

  const postActivity = async (body: string, workflowChoice: string) => {
    if (!taskId || !rootId) {
      throw new Error('Task ID and Root ID are required');
    }

    const response = await fetch(`/api/tasks/${taskId}/activities`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        root_id: rootId,
        body,
        workflow_choice: workflowChoice,
      }),
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(`HTTP ${response.status}: ${text}`);
    }

    // 刷新活动列表
    await fetchActivities();
  };

  useEffect(() => {
    fetchActivities();
  }, [taskId, rootId]);

  return {
    activities,
    loading,
    error,
    refresh: fetchActivities,
    postActivity,
  };
}

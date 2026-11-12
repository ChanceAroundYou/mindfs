import React from 'react';
import { ActivityStream } from './ActivityStream';
import { ActivityInput } from './ActivityInput';
import { useTaskActivities } from './useTaskActivities';
import './ActivityStream.css';

interface ConversationTaskViewProps {
  taskId: string;
  rootId: string;
}

export const ConversationTaskView: React.FC<ConversationTaskViewProps> = ({
  taskId,
  rootId,
}) => {
  const { activities, loading, error, postActivity } = useTaskActivities(
    taskId,
    rootId
  );

  if (loading && activities.length === 0) {
    return (
      <div style={{ padding: '24px', textAlign: 'center' }}>
        加载中...
      </div>
    );
  }

  if (error) {
    return (
      <div style={{ padding: '24px', color: '#d32f2f' }}>
        加载失败: {error}
      </div>
    );
  }

  return (
    <div className="conversation-task-view">
      <ActivityStream activities={activities} />
      <ActivityInput
        taskId={taskId}
        rootId={rootId}
        onSubmit={postActivity}
      />
    </div>
  );
};

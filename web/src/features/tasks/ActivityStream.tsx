import React from 'react';
import { TaskActivity } from '../../shared/taskComments';

interface ActivityStreamProps {
  activities: TaskActivity[];
}

export const ActivityStream: React.FC<ActivityStreamProps> = ({ activities }) => {
  if (activities.length === 0) {
    return (
      <div className="activity-stream-empty">
        <p>还没有留言</p>
      </div>
    );
  }

  return (
    <div className="activity-stream">
      {activities.map((activity) => (
        <ActivityCard key={activity.id} activity={activity} />
      ))}
    </div>
  );
};

interface ActivityCardProps {
  activity: TaskActivity;
}

const ActivityCard: React.FC<ActivityCardProps> = ({ activity }) => {
  const isUser = activity.role === 'user';

  return (
    <div className={`activity-card activity-card--${activity.role}`}>
      <div className="activity-card__header">
        <span className="activity-card__author">
          {isUser ? '用户' : 'Agent'}
        </span>
        <span className="activity-card__time">
          {new Date(activity.created_at).toLocaleString('zh-CN')}
        </span>
      </div>
      <div className="activity-card__body">
        {activity.body}
      </div>
      {activity.workflow_choice && activity.workflow_choice !== 'empty' && (
        <div className="activity-card__meta">
          <span className="activity-card__workflow">
            Workflow: {activity.workflow_choice}
          </span>
        </div>
      )}
    </div>
  );
};

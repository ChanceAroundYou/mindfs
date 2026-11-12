import React, { useState } from 'react';

interface ActivityInputProps {
  taskId: string;
  rootId: string;
  onSubmit: (body: string, workflowChoice: string) => Promise<void>;
  disabled?: boolean;
}

export const ActivityInput: React.FC<ActivityInputProps> = ({
  taskId,
  rootId,
  onSubmit,
  disabled = false,
}) => {
  const [body, setBody] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    if (!body.trim() || submitting) {
      return;
    }

    setSubmitting(true);
    try {
      await onSubmit(body.trim(), 'empty');
      setBody('');
    } catch (error) {
      console.error('Failed to post comment:', error);
      alert('发送失败，请重试');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form className="activity-input" onSubmit={handleSubmit}>
      <textarea
        className="activity-input__textarea"
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder="输入留言..."
        disabled={disabled || submitting}
        rows={3}
      />
      <div className="activity-input__actions">
        <button
          type="submit"
          className="activity-input__submit"
          disabled={!body.trim() || submitting || disabled}
        >
          {submitting ? '发送中...' : '发送'}
        </button>
      </div>
    </form>
  );
};

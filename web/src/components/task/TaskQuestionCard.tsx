import React from "react";
import { AskUserQuestionCard } from "../session/AskUserQuestionCard";
import type { ToolCall } from "../../shared/sessionTypes";
import type { TaskActivity } from "../../shared/taskComments";
import { normalizeActivityStatus, parseTaskActivityPayload } from "../../shared/activityModel";

export type TaskQuestionAnswers = Record<string, string>;

export type TaskQuestionCardProps = {
  activity: TaskActivity;
  /** 卡片所属 root；活动行自己没带 root_id 时用面板的。 */
  rootId?: string;
  onAnswer?: (activity: TaskActivity, answers: TaskQuestionAnswers) => void | Promise<void>;
};

/**
 * ask_user 落到任务对话后的回答卡。
 *
 * 复用会话时间线里的 AskUserQuestionCard，而不是重写一套选项/输入/校验：两处
 * 的问题结构、多选语义和提交按钮都必须是同一份实现。这里只负责把 TaskActivity
 * 的 payload 翻译成 ToolCall，并在提交后把答案交给 hook 走 WebSocket 回答链路。
 */
export function TaskQuestionCard({ activity, rootId, onAnswer }: TaskQuestionCardProps) {
  const payload = parseTaskActivityPayload(activity);
  const toolUseId =
    `${activity.tool_use_id || payload.tool_use_id || ""}`.trim();
  const normalized = normalizeActivityStatus(activity.status);
  const waiting = normalized === "waiting_user";
  // cancelled/failed 的卡已经不可能再答：卡片要显示「已关闭」而不是和无答案的
  // completed 混在一起（超时收口、被新提问取代都走这条路）。
  const closed = normalized === "cancelled" || normalized === "failed";
  const toolCall: ToolCall = {
    callId: toolUseId,
    title: activity.body,
    status: waiting ? "running" : closed ? "cancelled" : "complete",
    kind: "ask_user",
    meta: {
      questions: payload.questions,
      input: payload.input,
      answers: payload.answers,
      title: payload.title,
      toolUseId,
    },
  };
  const targetRoot = activity.root_id || rootId;

  return (
    <div data-testid="task-question-card" data-activity-id={activity.id}>
      <AskUserQuestionCard
        toolCall={toolCall}
        rootId={targetRoot}
        sessionKey={activity.session_key}
        agent={typeof payload.agent === "string" ? payload.agent : undefined}
        active={waiting}
        statusNote={activity.summary}
        onAnswer={
          onAnswer
            ? ({ answers }) => onAnswer(activity, answers)
            : undefined
        }
      />
    </div>
  );
}
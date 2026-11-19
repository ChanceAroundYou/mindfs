import React from "react";
import { useI18n } from "../../i18n/index";
import { WorkflowIcon } from "./taskIcons";

// 任务详情页的「跳到工作流会话」按钮。
//
// 工作流节点在各自的 agent 会话里干活，任务对话只放汇报。没有这个入口时，
// 想看节点实际做了什么必须先展开运行视图、再逐节点点「打开会话」。
//
// 任务 #87 第 4 条：它原来是一枚带文字的独立胶囊，和运行视图里的逐节点入口
// 抢位置。现在收成一枚图标按钮（复用已有的 WorkflowIcon），名字放在 title /
// aria-label 里 —— 位置不变、信息不减，视觉噪音没了。
//
// 只负责展示：选哪条会话由 pickWorkflowSessionKey（纯函数）决定，落点与取数
// 都在 useRunViewEntry；按钮不自己路由。

export type WorkflowSessionJumpProps = {
  sessionKey: string;
  loading: boolean;
  onOpenSession: (sessionKey: string) => void;
};

export function WorkflowSessionJump({ sessionKey, loading, onOpenSession }: WorkflowSessionJumpProps) {
  const { t } = useI18n();
  const hasSession = sessionKey !== "";
  // 首次取节点行时还没有会话键：这时不能报「暂无工作流会话」（它可能马上就有），
  // 只能说加载中。已经取到的键不会因重取而失效 —— NodeRun 的 session_key 一旦
  // 绑定就不再变化，所以刷新期间按钮保持可点，免得任务每更新一次就闪一下禁用。
  const label = hasSession
    ? t("task.runTimeline.openWorkflowSession")
    : loading
      ? t("common.loading")
      : t("task.runTimeline.noWorkflowSession");

  return (
    <button
      type="button"
      data-testid="workflow-session-jump"
      disabled={!hasSession}
      onClick={() => {
        if (!hasSession) return;
        onOpenSession(sessionKey);
      }}
      title={label}
      aria-label={label}
      style={hasSession ? buttonStyle : disabledStyle}
    >
      <WorkflowIcon size={13} />
    </button>
  );
}

const buttonStyle: React.CSSProperties = {
  alignSelf: "flex-start",
  width: 24,
  height: 24,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
  borderRadius: 6,
  padding: 0,
  cursor: "pointer",
  flexShrink: 0,
};

const disabledStyle: React.CSSProperties = {
  ...buttonStyle,
  color: "var(--text-secondary)",
  opacity: 0.45,
  cursor: "not-allowed",
};

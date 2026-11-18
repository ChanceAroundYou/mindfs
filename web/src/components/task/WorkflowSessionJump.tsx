import React from "react";
import { useI18n } from "../../i18n/index";

// 任务详情页的「跳到工作流会话」按钮。
//
// 工作流节点在各自的 agent 会话里干活，任务对话只放汇报。没有这个入口时，
// 想看节点实际做了什么必须先展开运行视图、再逐节点点「打开会话」。这里把
// 「最值得看的那条会话」提到详情页顶层，一次点击直达。
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
      style={hasSession ? buttonStyle : disabledStyle}
    >
      {label}
    </button>
  );
}

const buttonStyle: React.CSSProperties = {
  alignSelf: "flex-start",
  border: "1px solid var(--accent-color)",
  background: "rgba(37, 99, 235, 0.10)",
  color: "var(--accent-color)",
  borderRadius: 6,
  padding: "3px 8px",
  fontSize: 12,
  fontWeight: 700,
  cursor: "pointer",
};

const disabledStyle: React.CSSProperties = {
  ...buttonStyle,
  borderColor: "var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
  fontWeight: 400,
  cursor: "not-allowed",
};

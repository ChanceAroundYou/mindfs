import React from "react";
import { useI18n } from "../../i18n/index";
import { panelButtonStyle, panelSurfaceStyle } from "../shell/PanelShell";
import { PromptEditor } from "../editor/PromptEditor";
import { WorkflowProposalReview } from "./workflowAuthoring/WorkflowProposalReview";
import type { useWorkflowAuthoring } from "./useWorkflowAuthoring";
import type { WorkflowAuthoringApplyResult, useWorkflowAuthoringReview } from "./useWorkflowAuthoringReview";

// 「AI 生成 / 提案」这一块从 WorkflowDraftEditor 拆出来（只为守住 500 行硬线）。
// 它自己不带状态：展开/收起、指令文本与生成流程都留在编辑器里，这里只负责排版。
// 手机上不套内圈边框（用户 2026-11-20：「大框套小框」），桌面端保留分组感。

export type WorkflowAuthoringPanelProps = {
  isMobile: boolean;
  expanded: boolean;
  onToggle: () => void;
  createMode: boolean;
  instruction: string;
  onInstructionChange: (text: string) => void;
  /** 切换生成对象时重置编辑器内容（新建流程 / 当前工作流）。 */
  resetKey: string;
  onGenerate: () => void;
  generating: boolean;
  /** 画布有未保存改动时不许生成：提案要绑定确定的修订。 */
  dirty: boolean;
  authoring: ReturnType<typeof useWorkflowAuthoring>;
  review: ReturnType<typeof useWorkflowAuthoringReview>;
  onApplied: (result: WorkflowAuthoringApplyResult) => void;
};

export function WorkflowAuthoringPanel({
  isMobile,
  expanded,
  onToggle,
  createMode,
  instruction,
  onInstructionChange,
  resetKey,
  onGenerate,
  generating,
  dirty,
  authoring,
  review,
  onApplied,
}: WorkflowAuthoringPanelProps) {
  const { t } = useI18n();
  return (
    <section
      data-testid="workflow-authoring-panel"
      style={{
        ...(isMobile ? {} : panelSurfaceStyle()),
        display: "flex",
        flexDirection: "column",
        gap: 6,
        fontSize: 12,
      }}
    >
      {/* 手机上这一块默认收起：输入框 + 提示会把画布挤到首屏以下，而手机上第一眼
          要看的是图。生成出提案/报错/新建流程时由父层自动展开，绝不把结果藏起来。 */}
      {isMobile ? (
        <button
          type="button"
          data-testid="workflow-authoring-toggle"
          aria-expanded={expanded}
          onClick={onToggle}
          style={{ ...panelButtonStyle("secondary"), width: "100%", display: "flex", justifyContent: "space-between", alignItems: "center" }}
        >
          <span>{t("task.workflowEditor.aiAssist")}</span>
          <span aria-hidden>{expanded ? "▾" : "▸"}</span>
        </button>
      ) : (
        // 桌面端一直展开，标题行放在输入框上面（用户 2026-11-20 要求把
        // 「AI 生成 / 提案」这行加回来），与手机上的折叠按钮同一个文案。
        <div style={{ fontSize: 11, fontWeight: 700, color: "var(--text-secondary)" }}>{t("task.workflowEditor.aiAssist")}</div>
      )}
      {expanded ? (
        <>
          {createMode ? <div data-testid="workflow-authoring-create-hint">{t("task.workflowAuthoring.createHint")}</div> : null}
          <PromptEditor
            value={instruction}
            onChange={onInstructionChange}
            resetKey={resetKey}
            placeholder={t("task.workflowAuthoring.instructionPlaceholder")}
            onSend={onGenerate}
            sending={generating}
            sendDisabled={dirty || !instruction.trim()}
            containerTestId="workflow-authoring-instruction"
            sendTestId="workflow-authoring-generate"
            sendLabel={t("task.workflowAuthoring.generate")}
          />
          <WorkflowProposalReview authoring={authoring} review={review} disabled={dirty} onApplied={onApplied} />
        </>
      ) : null}
    </section>
  );
}

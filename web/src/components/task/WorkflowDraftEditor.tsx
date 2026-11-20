import React, { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { useResponsive } from "../../hooks/useResponsive";
import { useBackLayer } from "../../hooks/useBackLayer";
import { panelButtonStyle, panelInputStyle, panelSurfaceStyle } from "../shell/PanelShell";
import { PromptEditor } from "../editor/PromptEditor";
import { PlusIcon } from "../action/composerIcons";
import { DeleteIcon, EditPencilIcon } from "./taskIcons";
import { WorkflowCanvas } from "./WorkflowCanvas";
import { useWorkflowAuthoring } from "./useWorkflowAuthoring";
import { useWorkflowAuthoringReview } from "./useWorkflowAuthoringReview";
import { useWorkflowDraftEditor } from "./useWorkflowDraftEditor";
import { WorkflowProposalReview } from "./workflowAuthoring/WorkflowProposalReview";
import { workflowDraftDisplayName } from "../../shared/workflowModel";
import { authoringPanelExpanded, draftPanelFrame } from "./workflowEditorLayout";
import type { AgentStatus } from "../../shared/agentTypes";

export type WorkflowDraftEditorProps = {
  onClose: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  agents?: AgentStatus[];
};

// 独立于任务详情的 Draft 编辑页；从看板工具栏进入。
// 取数与写操作都在 useWorkflowDraftEditor（.ts，允许 import services）；本视图只管渲染。
export function WorkflowDraftEditor({ onClose, onDirtyChange, agents = [] }: WorkflowDraftEditorProps) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const e = useWorkflowDraftEditor(onDirtyChange);
  const authoring = useWorkflowAuthoring();
  const review = useWorkflowAuthoringReview(authoring, {});
  const [instruction, setInstruction] = useState("");
  const [createMode, setCreateMode] = useState(false);
  const [authoringOpen, setAuthoringOpen] = useState(false);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [createDialogKey, setCreateDialogKey] = useState(0);
  const [metaDialogOpen, setMetaDialogOpen] = useState(false);
  const [metaName, setMetaName] = useState("");
  const [metaDescription, setMetaDescription] = useState("");
  // 手机草稿列表是浮在画布上的抽屉：进编辑器先展开让人选草稿，选完自动收起，
  // 之后靠左边缘的悬浮把手再打开。桌面恒为固定竖栏，这个状态不参与。
  const [draftPanelOpen, setDraftPanelOpen] = useState(true);
  const createIdRef = useRef("");
  const { observeRevision, reset } = authoring;
  const previousDraftRef = useRef("");

  useEffect(() => {
    if (createMode) return;
    if (!e.selectedId || !e.revision) return;
    observeRevision({ scope: "draft", resourceID: e.selectedId, revision: e.revision });
  }, [createMode, e.selectedId, e.revision, observeRevision]);

  // A proposal is bound to the draft it was generated for. Leaving that draft
  // must discard it rather than leave a ready patch pointed at the old target.
  useEffect(() => {
    if (previousDraftRef.current && previousDraftRef.current !== e.selectedId) {
      reset();
      setInstruction("");
    }
    previousDraftRef.current = e.selectedId;
  }, [e.selectedId, reset]);

  const generate = (text?: string) => {
    const targetID = createMode ? createIdRef.current : e.selected?.workflow_id;
    const instructionText = (text ?? instruction).trim();
    if (!targetID || !instructionText) return;
    setInstruction(instructionText);
    void authoring.generate({
      scope: createMode ? "create" : "draft",
      resourceID: targetID,
      revision: createMode ? "r0" : e.revision,
      instruction: instructionText,
    }).then((result) => {
      if (result.conflictRevision && !createMode) void e.reloadSelected();
    });
  };

  // Creating through the Agent reviews against the deterministic r0 graph and
  // persists nothing until the accepted subset is applied as r1. The id is
  // fixed for the whole create session so generation and apply name one draft.
  const openCreateDialog = () => {
    reset();
    setInstruction("");
    setCreateDialogKey((key) => key + 1);
    setCreateDialogOpen(true);
  };

  const closeCreateDialog = () => {
    setCreateDialogOpen(false);
    setInstruction("");
  };

  const startCreate = (text: string) => {
    const instructionText = (text || instruction).trim();
    if (!instructionText) return;
    reset();
    createIdRef.current = `workflow-${Date.now().toString(36)}`;
    setInstruction(instructionText);
    setCreateDialogOpen(false);
    setCreateMode(true);
    void authoring.generate({
      scope: "create",
      resourceID: createIdRef.current,
      revision: "r0",
      instruction: instructionText,
    });
  };

  const createBlank = () => {
    setCreateDialogOpen(false);
    setInstruction("");
    void e.create();
  };

  const cancelCreate = () => {
    setCreateMode(false);
    reset();
    setInstruction("");
  };

  const openMetaDialog = () => {
    if (!e.selected) return;
    setMetaName(workflowDraftDisplayName(e.selected));
    setMetaDescription(e.selected.description || "");
    setMetaDialogOpen(true);
  };

  const closeMetaDialog = () => {
    setMetaName("");
    setMetaDescription("");
    setMetaDialogOpen(false);
  };

  const submitMeta = async () => {
    if (!e.selected) return;
    if (await e.updateMeta(e.selected, { name: metaName, description: metaDescription })) {
      closeMetaDialog();
    }
  };

  // 手机上 AI 面板默认收起；提案/报错/新建流程一出现就必须可见。
  const authoringExpanded = authoringPanelExpanded({
    isMobile,
    open: authoringOpen,
    createMode,
    hasProposal: !!authoring.state.proposal,
    hasError: !!authoring.state.error,
  });

  const save = () => void e.save();
  const publish = () => void e.publish();
  const close = () => {
    void e.confirmLeave().then((ok) => { if (ok) onClose(); });
  };
  // 安卓返回键/侧滑关编辑器，而不是把人从看板这一层直接退走。组件只在打开时挂载，
  // 所以 open 恒为 true；关闭路径复用上面那条（脏改动先确认）。
  useBackLayer(true, close);

  // 手机上把主区（DefaultListView 的滚动容器）右侧滚动条藏起来：编辑器自己
  // 的窄屏滚动发生在那一层，浏览器滚动条会贴着屏幕右缘吃掉几个像素，而这块
  // 界面没有「滚动位置」语义（画布自带固定高）。只在编辑器挂载期间生效。
  useEffect(() => {
    if (!isMobile) return;
    document.body.dataset.workflowEditorMobile = "true";
    return () => {
      delete document.body.dataset.workflowEditorMobile;
    };
  }, [isMobile]);

  return (
    <div
      data-testid="workflow-draft-editor"
      style={{
        display: "flex",
        flexDirection: "column",
        // 手机上编辑器整页纵向滚动（画布有自己的固定高），桌面端维持「一屏铺满、
        // 只有草稿列表和画布各自内部滚动」。
        height: isMobile ? "auto" : "100%",
        minHeight: isMobile ? "100%" : 0,
        gap: 10,
        padding: isMobile ? 8 : 12,
      }}
    >
      {isMobile ? (
        <style>{`body[data-workflow-editor-mobile="true"] .mindfs-main-scroll{scrollbar-width:none;-ms-overflow-style:none}body[data-workflow-editor-mobile="true"] .mindfs-main-scroll::-webkit-scrollbar{display:none;width:0;height:0}`}</style>
      ) : null}
      {/* 新建工作流：唯一的创建入口，输入框与任务面板同源（PromptEditor）。 */}
      {createDialogOpen ? (
        <div
          data-testid="workflow-create-dialog"
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(0, 0, 0, 0.5)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: 16,
            zIndex: 1000,
          }}
        >
          <div style={{ ...panelSurfaceStyle(), width: "min(540px, 100%)", padding: 16, display: "flex", flexDirection: "column", gap: 12 }}>
            <div style={{ fontSize: 14, fontWeight: 600 }}>{t("task.workflowEditor.newWorkflow")}</div>
            <PromptEditor
              value={instruction}
              onChange={setInstruction}
              resetKey={createDialogKey}
              placeholder={t("task.workflowAuthoring.createDialogPlaceholder")}
              onSend={startCreate}
              sendDisabled={!instruction.trim()}
              sendTestId="workflow-agent-create"
              sendLabel={t("task.workflowAuthoring.startCreate")}
            />
            <div style={{ fontSize: 11, color: "var(--text-secondary)" }}>
              {t("task.workflowAuthoring.createHint")}
            </div>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", flexWrap: "wrap" }}>
              <button type="button" style={panelButtonStyle("secondary")} onClick={closeCreateDialog}>{t("common.cancel")}</button>
              <button type="button" style={panelButtonStyle("secondary")} disabled={e.saving} onClick={createBlank}>{t("task.workflowEditor.createFromBlank")}</button>
            </div>
          </div>
        </div>
      ) : null}

      {/* 名称/描述是 WorkflowMeta（控制面），与画布草稿修订解耦，所以单独弹窗编辑。 */}
      {metaDialogOpen && e.selected ? (
        <div
          data-testid="workflow-meta-dialog"
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(0, 0, 0, 0.5)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: 16,
            zIndex: 1000,
          }}
        >
          <div style={{ ...panelSurfaceStyle(), width: "min(460px, 100%)", padding: 16, display: "flex", flexDirection: "column", gap: 10 }}>
            <div style={{ fontSize: 14, fontWeight: 600 }}>{t("task.workflowEditor.editMeta")}</div>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
              {t("task.workflowEditor.name")}
              <input
                data-testid="workflow-meta-name"
                value={metaName}
                onChange={(event) => setMetaName(event.target.value)}
                style={panelInputStyle()}
              />
            </label>
            <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
              {t("task.workflowEditor.description")}
              <textarea
                data-testid="workflow-meta-description"
                value={metaDescription}
                placeholder={t("task.workflowEditor.descriptionPlaceholder")}
                onChange={(event) => setMetaDescription(event.target.value)}
                rows={3}
                style={{ ...panelInputStyle(), resize: "vertical" }}
              />
            </label>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
              <button type="button" style={panelButtonStyle("secondary")} onClick={closeMetaDialog}>{t("common.cancel")}</button>
              <button
                type="button"
                data-testid="workflow-meta-save"
                style={panelButtonStyle("primary")}
                disabled={e.metaBusy || !metaName.trim()}
                onClick={() => void submitMeta()}
              >
                {e.metaBusy ? t("task.workflowEditor.saving") : t("task.workflowEditor.save")}
              </button>
            </div>
          </div>
        </div>
      ) : null}

      <div style={{ display: "flex", flexDirection: isMobile ? "column" : "row", alignItems: isMobile ? "stretch" : "flex-start", gap: 8 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0, flex: 1 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
            <strong style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {createMode
                ? t("task.workflowAuthoring.createTitle")
                : e.selected ? workflowDraftDisplayName(e.selected) : t("task.workflowEditor.title")}
            </strong>
            {e.dirty ? <span data-testid="workflow-draft-dirty" style={{ width: 8, height: 8, borderRadius: "50%", background: "var(--danger-color,#ef4444)" }} /> : null}
            {!createMode && e.selected ? (
              <button
                type="button"
                data-testid="workflow-draft-edit-meta"
                aria-label={t("task.workflowEditor.editMeta")}
                title={t("task.workflowEditor.editMeta")}
                onClick={openMetaDialog}
                style={{ ...panelButtonStyle("secondary"), padding: "2px 6px", minWidth: "unset", fontSize: 11, display: "inline-flex", alignItems: "center", justifyContent: "center" }}
              >
                <EditPencilIcon />
              </button>
            ) : null}
          </div>
          {!createMode && e.selected?.description ? (
            <div
              data-testid="workflow-draft-description"
              // 手机上描述被单行省略号截断过（用户 2026-11-20 报「描述被遮挡截断」）：
              // 窄屏给它两行完整宽度，桌面维持单行（顶栏高度固定）。
              style={{
                fontSize: 11,
                color: "var(--text-secondary)",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: isMobile ? "normal" : "nowrap",
                ...(isMobile
                  ? { display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical" as const }
                  : {}),
              }}
            >
              {e.selected.description}
            </div>
          ) : null}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", justifyContent: isMobile ? "flex-start" : "flex-end" }}>
          {createMode ? (
            <button type="button" style={panelButtonStyle("secondary")} onClick={cancelCreate}>{t("task.workflowAuthoring.cancelCreate")}</button>
          ) : (
            <>
              <button type="button" style={panelButtonStyle("primary")} disabled={e.saving || !e.dirty} onClick={save}>{e.saving ? t("task.workflowEditor.saving") : t("task.workflowEditor.save")}</button>
              <button type="button" style={panelButtonStyle("secondary")} disabled={e.saving || e.dirty || !e.selected} onClick={publish}>{t("task.workflowEditor.publish")}</button>
            </>
          )}
          <button type="button" style={panelButtonStyle("secondary")} onClick={close}>{t("task.workflowEditor.close")}</button>
        </div>
      </div>
      {e.error ? <div role="alert" style={{ color: "var(--danger-color,#ef4444)", fontSize: 12 }}>{e.error}</div> : null}
      {!e.loading && (e.selected || createMode) ? (
        <section data-testid="workflow-authoring-panel" style={{ ...panelSurfaceStyle(), display: "flex", flexDirection: "column", gap: 6, fontSize: 12 }}>
          {/* 手机上这一块默认收起：输入框 + 提示会把画布挤到首屏以下，而手机上第一眼
              要看的是图。生成出提案/报错/新建流程时自动展开，绝不把结果藏起来。 */}
          {isMobile ? (
            <button
              type="button"
              data-testid="workflow-authoring-toggle"
              aria-expanded={authoringExpanded}
              onClick={() => setAuthoringOpen((open) => !open)}
              style={{ ...panelButtonStyle("secondary"), width: "100%", display: "flex", justifyContent: "space-between", alignItems: "center" }}
            >
              <span>{t("task.workflowEditor.aiAssist")}</span>
              <span aria-hidden>{authoringExpanded ? "▾" : "▸"}</span>
            </button>
          ) : null}
          {authoringExpanded ? (
            <>
              {createMode ? <div data-testid="workflow-authoring-create-hint">{t("task.workflowAuthoring.createHint")}</div> : null}
              <PromptEditor
                value={instruction}
                onChange={setInstruction}
                resetKey={createMode ? createIdRef.current : e.selectedId}
                placeholder={t("task.workflowAuthoring.instructionPlaceholder")}
                onSend={generate}
                sending={authoring.state.phase === "generating"}
                sendDisabled={e.dirty || !instruction.trim()}
                containerTestId="workflow-authoring-instruction"
                sendTestId="workflow-authoring-generate"
                sendLabel={t("task.workflowAuthoring.generate")}
              />
              <WorkflowProposalReview
                authoring={authoring}
                review={review}
                disabled={e.dirty}
                onApplied={(result) => {
                  if (result.draft) {
                    setCreateMode(false);
                    e.adoptDraft(result.draft);
                  }
                }}
              />
            </>
          ) : null}
        </section>
      ) : null}
      {e.loading ? <div>{t("task.workflowEditor.loading")}</div> : (
        <div style={{ position: isMobile ? "relative" : undefined, display: "flex", flexDirection: isMobile ? "column" : "row", minHeight: 0, flex: isMobile ? "none" : 1, gap: isMobile ? 8 : 12 }}>
          {/* 手机上草稿列表是浮在画布上方的左侧抽屉（竖向），收起后完全隐藏、
              只留左边缘一个悬浮把手；展开时覆盖画布但**不挤压**它的宽度。
              桌面仍是固定 190px 竖栏。 */}
          {isMobile && !draftPanelOpen ? (
            <button
              type="button"
              data-testid="workflow-draft-panel-toggle"
              aria-label={t("task.workflowEditor.drafts")}
              aria-expanded={false}
              title={t("task.workflowEditor.drafts")}
              onClick={() => setDraftPanelOpen(true)}
              style={{
                ...panelButtonStyle("secondary"),
                position: "absolute",
                left: 0,
                top: 8,
                zIndex: 30,
                width: 28,
                height: 44,
                padding: 0,
                borderTopRightRadius: 10,
                borderBottomRightRadius: 10,
                border: "1px solid var(--border-color)",
                borderLeft: "none",
                background: "var(--panel-bg, var(--button-bg))",
                color: "var(--text-secondary)",
                boxShadow: "0 4px 12px rgba(0, 0, 0, 0.2)",
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: 12,
              }}
            >
              ▸
            </button>
          ) : null}
          <nav
            aria-label={t("task.workflowEditor.drafts")}
            data-testid="workflow-draft-panel"
            data-open={isMobile ? (draftPanelOpen ? "true" : "false") : undefined}
            style={{
              ...panelSurfaceStyle(),
              ...draftPanelFrame({ isMobile, open: draftPanelOpen }),
              padding: 8,
              alignItems: "stretch",
              gap: isMobile ? 6 : 4,
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: isMobile ? 0 : 2, flexShrink: 0 }}>
              <span style={{ flex: 1, fontSize: 11, fontWeight: 700, color: "var(--text-secondary)" }}>{t("task.workflowEditor.drafts")}</span>
              {isMobile ? (
                <button
                  type="button"
                  data-testid="workflow-draft-panel-collapse"
                  aria-label={t("task.workflowEditor.collapseDrafts")}
                  title={t("task.workflowEditor.collapseDrafts")}
                  onClick={() => setDraftPanelOpen(false)}
                  style={{ ...panelButtonStyle("secondary"), height: 24, padding: "0 8px", fontSize: 12, display: "inline-flex", alignItems: "center", justifyContent: "center" }}
                >
                  ◂
                </button>
              ) : null}
              <button
                type="button"
                data-testid="workflow-create-open"
                aria-label={t("task.workflowEditor.newWorkflow")}
                title={t("task.workflowEditor.newWorkflow")}
                disabled={e.saving}
                onClick={openCreateDialog}
                style={{ ...panelButtonStyle("secondary"), height: 24, padding: "0 8px", fontSize: 12, display: "inline-flex", alignItems: "center", justifyContent: "center" }}
              >
                <PlusIcon size={13} />
              </button>
            </div>
            {e.drafts.map((d) => {
              const displayName = workflowDraftDisplayName(d);
              const isSelected = d.workflow_id === e.selectedId;
              return (
                <div key={d.workflow_id} style={{ display: "flex", gap: 4, alignItems: "center", flexShrink: 0, maxWidth: isMobile ? 220 : undefined }}>
                  {/* 描述不进卡片：190px 宽的列表里挤两行字，名字反而被压到看不清。
                      想读描述把鼠标停在卡上看 title；单行名字仍然省略号截断。 */}
                  <button
                    type="button"
                    style={{ ...panelButtonStyle(isSelected ? "primary" : "secondary"), display: "block", textAlign: "left", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                    aria-current={isSelected}
                    onClick={() => { setCreateMode(false); if (isMobile) setDraftPanelOpen(false); void e.choose(d); }}
                    title={d.description ? `${displayName} (${d.revision}) — ${d.description}` : `${displayName} (${d.revision})`}
                  >
                    {displayName}
                  </button>
                  <button
                    type="button"
                    data-testid="workflow-draft-delete"
                    data-workflow-id={d.workflow_id}
                    aria-label={t("task.workflowEditor.delete")}
                    title={t("task.workflowEditor.delete")}
                    disabled={!!e.deletingId || e.saving}
                    onClick={() => void e.remove(d)}
                    style={{ ...panelButtonStyle("secondary"), padding: "4px 6px", minWidth: "unset", fontSize: 11, color: "#dc2626", display: "inline-flex", alignItems: "center", justifyContent: "center" }}
                  >
                    <DeleteIcon />
                  </button>
                </div>
              );
            })}
            {e.drafts.length === 0 ? <span style={{ flexShrink: 0 }}>{t("task.workflowEditor.empty")}</span> : null}
          </nav>
          <div style={{ flex: 1, minWidth: 0, minHeight: 0, overflow: isMobile ? "visible" : "auto" }}>
            {createMode ? null : e.selected ? (
              <WorkflowCanvas draftId={e.selected.workflow_id} graph={e.graph} dirty={e.dirty} onChange={e.setGraph} agents={agents} />
            ) : <div>{t("task.workflowEditor.selectOrCreate")}</div>}
          </div>
        </div>
      )}
    </div>
  );
}
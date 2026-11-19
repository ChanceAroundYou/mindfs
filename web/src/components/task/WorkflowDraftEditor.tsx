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
import { authoringPanelExpanded } from "./workflowEditorLayout";
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

      <div style={{ display: "flex", alignItems: "flex-start", gap: 8 }}>
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
              style={{ fontSize: 11, color: "var(--text-secondary)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
            >
              {e.selected.description}
            </div>
          ) : null}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", justifyContent: "flex-end" }}>
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
        <div style={{ display: "flex", flexDirection: isMobile ? "column" : "row", minHeight: 0, flex: isMobile ? "none" : 1, gap: isMobile ? 8 : 12 }}>
          {/* 手机上草稿列表变成横向一条：固定 190px 的竖栏在 390px 屏上会吃掉一半宽度，
              画布只剩巴掌大。横向条只留「当前草稿 + 切走 + 新建/删除」。 */}
          <nav
            aria-label={t("task.workflowEditor.drafts")}
            style={{
              ...panelSurfaceStyle(),
              padding: 8,
              display: "flex",
              flexDirection: isMobile ? "row" : "column",
              alignItems: isMobile ? "center" : "stretch",
              gap: isMobile ? 6 : 4,
              width: isMobile ? "100%" : 190,
              overflowX: isMobile ? "auto" : undefined,
              overflowY: isMobile ? undefined : "auto",
              flexShrink: 0,
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: isMobile ? 0 : 2, flexShrink: 0 }}>
              <span style={{ flex: isMobile ? "none" : 1, display: isMobile ? "none" : "block", fontSize: 11, fontWeight: 700, color: "var(--text-secondary)" }}>{t("task.workflowEditor.drafts")}</span>
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
                    onClick={() => { setCreateMode(false); void e.choose(d); }}
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
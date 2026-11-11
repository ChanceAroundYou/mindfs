import React, { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { useResponsive } from "../../hooks/useResponsive";
import { WorkflowCanvas } from "./WorkflowCanvas";
import { useWorkflowAuthoring } from "./useWorkflowAuthoring";
import { useWorkflowAuthoringReview } from "./useWorkflowAuthoringReview";
import { useWorkflowDraftEditor } from "./useWorkflowDraftEditor";
import { WorkflowProposalReview } from "./workflowAuthoring/WorkflowProposalReview";

export type WorkflowDraftEditorProps = {
  onClose: () => void;
  onDirtyChange?: (dirty: boolean) => void;
};

// 独立于任务详情的 Draft 编辑页；从看板工具栏进入。
// 取数与写操作都在 useWorkflowDraftEditor（.ts，允许 import services）；本视图只管渲染。
export function WorkflowDraftEditor({ onClose, onDirtyChange }: WorkflowDraftEditorProps) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const e = useWorkflowDraftEditor(onDirtyChange);
  const authoring = useWorkflowAuthoring();
  const review = useWorkflowAuthoringReview(authoring, {});
  const [instruction, setInstruction] = useState("");
  const [createMode, setCreateMode] = useState(false);
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
    }
    previousDraftRef.current = e.selectedId;
  }, [e.selectedId, reset]);

  const generate = () => {
    const targetID = createMode ? createIdRef.current : e.selected?.workflow_id;
    if (!targetID) return;
    void authoring.generate({
      scope: createMode ? "create" : "draft",
      resourceID: targetID,
      revision: createMode ? "r0" : e.revision,
      instruction,
    }).then((result) => {
      if (result.conflictRevision && !createMode) void e.reloadSelected();
    });
  };

  // Creating through the Agent reviews against the deterministic r0 graph and
  // persists nothing until the accepted subset is applied as r1. The id is
  // fixed for the whole create session so generation and apply name one draft.
  const startCreate = () => {
    reset();
    setCreateMode(true);
    createIdRef.current = `workflow-${Date.now().toString(36)}`;
  };

  const cancelCreate = () => {
    setCreateMode(false);
    reset();
  };

  const save = () => void e.save();
  const publish = () => void e.publish();
  const close = () => {
    void e.confirmLeave().then((ok) => { if (ok) onClose(); });
  };

  return (
    <div data-testid="workflow-draft-editor" style={{ display: "flex", flexDirection: "column", minHeight: 0, height: "100%", gap: 10, padding: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <strong>{createMode ? t("task.workflowAuthoring.createTitle") : t("task.workflowEditor.title")}</strong>
        {e.dirty ? <span data-testid="workflow-draft-dirty" style={{ width: 8, height: 8, borderRadius: "50%", background: "var(--danger-color,#ef4444)" }} /> : null}
        <span style={{ flex: 1 }} />
        {createMode ? (
          <button type="button" onClick={cancelCreate}>{t("task.workflowAuthoring.cancelCreate")}</button>
        ) : (
          <>
            <button type="button" data-testid="workflow-agent-create" disabled={e.saving || e.dirty || isMobile} onClick={startCreate}>{t("task.workflowAuthoring.startCreate")}</button>
            <button type="button" disabled={e.saving || isMobile} onClick={() => void e.create()}>{t("task.workflowEditor.create")}</button>
            <button type="button" disabled={e.saving || !e.dirty || isMobile} onClick={save}>{e.saving ? t("task.workflowEditor.saving") : t("task.workflowEditor.save")}</button>
            <button type="button" disabled={e.saving || e.dirty || !e.selected || isMobile} onClick={publish}>{t("task.workflowEditor.publish")}</button>
          </>
        )}
        <button type="button" onClick={close}>{t("task.workflowEditor.close")}</button>
      </div>
      {e.error ? <div role="alert" style={{ color: "var(--danger-color,#ef4444)", fontSize: 12 }}>{e.error}</div> : null}
      {!e.loading && (e.selected || createMode) ? (
        <section data-testid="workflow-authoring-panel" style={{ display: "flex", flexDirection: "column", gap: 6, fontSize: 12 }}>
          {createMode ? <div data-testid="workflow-authoring-create-hint">{t("task.workflowAuthoring.createHint")}</div> : null}
          <textarea
            data-testid="workflow-authoring-instruction"
            aria-label={t("task.workflowAuthoring.title")}
            value={instruction}
            placeholder={t("task.workflowAuthoring.instructionPlaceholder")}
            disabled={e.dirty || authoring.state.phase === "generating"}
            onChange={(event) => setInstruction(event.target.value)}
            style={{ minHeight: 56 }}
          />
          <div style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap" }}>
            <button
              type="button"
              data-testid="workflow-authoring-generate"
              disabled={e.dirty || !instruction.trim() || authoring.state.phase === "generating" || isMobile}
              onClick={generate}
            >
              {authoring.state.phase === "generating" ? t("task.workflowAuthoring.generating") : t("task.workflowAuthoring.generate")}
            </button>
            {e.dirty ? <span>{t("task.workflowEditor.patchSaveFirst")}</span> : null}
            {isMobile ? <span>{t("task.workflowAuthoring.mobileHint")}</span> : null}
          </div>
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
        </section>
      ) : null}
      {!e.loading && e.selected && !createMode ? (
        <details data-testid="workflow-patch-panel" style={{ fontSize: 12 }}>
          <summary>{t("task.workflowEditor.patchImport")}</summary>
          <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 6 }}>
            <textarea
              data-testid="workflow-patch-text"
              aria-label={t("task.workflowEditor.patchImport")}
              value={e.patchText}
              placeholder={t("task.workflowEditor.patchPlaceholder")}
              disabled={e.dirty || e.patchBusy || isMobile}
              onChange={(event) => e.setPatchText(event.target.value)}
              style={{ minHeight: 120, fontFamily: "monospace", fontSize: 11 }}
            />
            <div style={{ display: "flex", gap: 6 }}>
              <button type="button" disabled={e.dirty || e.patchBusy || !e.patchText.trim()} onClick={() => void e.validatePatch()}>{t("task.workflowEditor.patchValidate")}</button>
              <button type="button" disabled={e.dirty || e.patchBusy || !e.patchText.trim()} onClick={() => void e.applyPatch()}>{t("task.workflowEditor.patchApply")}</button>
            </div>
            {e.dirty ? <div>{t("task.workflowEditor.patchSaveFirst")}</div> : null}
            {e.patchIssues.length > 0 ? (
              <ul data-testid="workflow-patch-issues" style={{ margin: 0, paddingLeft: 18, color: "var(--danger-color,#ef4444)" }}>
                {e.patchIssues.map((issue, index) => (
                  <li key={`${issue.operation_id || "op"}-${issue.field || "field"}-${index}`}>
                    {t("task.workflowEditor.patchIssueLine", { operation: issue.operation_id || "-", field: issue.field || "-", message: issue.message })}
                  </li>
                ))}
              </ul>
            ) : null}
            {e.patchDiff ? (
              <ul data-testid="workflow-patch-diff" style={{ margin: 0, paddingLeft: 18 }}>
                {e.patchDiff.operations.map((op, index) => (
                  <li key={`${op.operation_id}-${index}`}>{t("task.workflowEditor.patchDiffLine", { operation: op.operation_id, op: op.op, node: op.node_id || "-" })}</li>
                ))}
              </ul>
            ) : null}
          </div>
        </details>
      ) : null}
      {!e.loading && e.selected && !createMode ? (
        <details data-testid="workflow-proposals-panel" style={{ fontSize: 12 }}>
          <summary>{t("task.workflowEditor.proposalsTitle")}</summary>
          <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 6 }}>
            <div>
              <button type="button" data-testid="workflow-proposals-refresh" disabled={e.proposalBusy} onClick={() => void e.refreshProposals()}>
                {t("task.workflowEditor.proposalRefresh")}
              </button>
            </div>
            {e.proposals.length === 0 ? <div>{t("task.workflowEditor.proposalsEmpty")}</div> : (
              <ul style={{ margin: 0, paddingLeft: 18 }}>
                {e.proposals.map((proposal) => (
                  <li key={proposal.id} data-status={proposal.status} style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
                    <span>{t("task.workflowEditor.proposalLine", { revision: proposal.revision, proposer: proposal.proposer || "-", status: proposal.status })}</span>
                    {proposal.status === "pending" ? (
                      <button
                        type="button"
                        data-testid="workflow-proposal-confirm"
                        data-proposal-id={proposal.id}
                        disabled={e.dirty || e.proposalBusy || isMobile}
                        onClick={() => void e.confirmProposal(proposal)}
                      >
                        {t("task.workflowEditor.proposalConfirm")}
                      </button>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </details>
      ) : null}
      {e.loading ? <div>{t("task.workflowEditor.loading")}</div> : (
        <div style={{ display: "flex", minHeight: 0, flex: 1, gap: 12 }}>
          <nav aria-label={t("task.workflowEditor.drafts")} style={{ width: 190, overflowY: "auto", display: "flex", flexDirection: "column", gap: 4 }}>
            {e.drafts.map((d) => <button type="button" key={d.workflow_id} aria-current={d.workflow_id === e.selectedId} onClick={() => { setCreateMode(false); void e.choose(d); }}>{d.workflow_id} · {d.revision}</button>)}
            {e.drafts.length === 0 ? <span>{t("task.workflowEditor.empty")}</span> : null}
          </nav>
          <div style={{ flex: 1, minWidth: 0, minHeight: 0, overflow: "auto" }}>
            {createMode ? null : e.selected ? (
              <WorkflowCanvas draftId={e.selected.workflow_id} graph={e.graph} dirty={e.dirty} readOnly={isMobile} onChange={e.setGraph} />
            ) : <div>{t("task.workflowEditor.selectOrCreate")}</div>}
          </div>
        </div>
      )}
    </div>
  );
}

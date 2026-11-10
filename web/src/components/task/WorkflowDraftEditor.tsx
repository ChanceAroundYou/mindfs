import React from "react";
import { useI18n } from "../../i18n/index";
import { useResponsive } from "../../hooks/useResponsive";
import { WorkflowCanvas } from "./WorkflowCanvas";
import { useWorkflowDraftEditor } from "./useWorkflowDraftEditor";

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

  const save = () => void e.save();
  const publish = () => void e.publish();
  const close = () => {
    void e.confirmLeave().then((ok) => { if (ok) onClose(); });
  };

  return (
    <div data-testid="workflow-draft-editor" style={{ display: "flex", flexDirection: "column", minHeight: 0, height: "100%", gap: 10, padding: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <strong>{t("task.workflowEditor.title")}</strong>
        {e.dirty ? <span data-testid="workflow-draft-dirty" style={{ width: 8, height: 8, borderRadius: "50%", background: "var(--danger-color,#ef4444)" }} /> : null}
        <span style={{ flex: 1 }} />
        <button type="button" disabled={e.saving || isMobile} onClick={() => void e.create()}>{t("task.workflowEditor.create")}</button>
        <button type="button" disabled={e.saving || !e.dirty || isMobile} onClick={save}>{e.saving ? t("task.workflowEditor.saving") : t("task.workflowEditor.save")}</button>
        <button type="button" disabled={e.saving || e.dirty || !e.selected || isMobile} onClick={publish}>{t("task.workflowEditor.publish")}</button>
        <button type="button" onClick={close}>{t("task.workflowEditor.close")}</button>
      </div>
      {e.error ? <div role="alert" style={{ color: "var(--danger-color,#ef4444)", fontSize: 12 }}>{e.error}</div> : null}
      {e.loading ? <div>{t("task.workflowEditor.loading")}</div> : (
        <div style={{ display: "flex", minHeight: 0, flex: 1, gap: 12 }}>
          <nav aria-label={t("task.workflowEditor.drafts")} style={{ width: 190, overflowY: "auto", display: "flex", flexDirection: "column", gap: 4 }}>
            {e.drafts.map((d) => <button type="button" key={d.workflow_id} aria-current={d.workflow_id === e.selectedId} onClick={() => void e.choose(d)}>{d.workflow_id} · {d.revision}</button>)}
            {e.drafts.length === 0 ? <span>{t("task.workflowEditor.empty")}</span> : null}
          </nav>
          <div style={{ flex: 1, minWidth: 0, minHeight: 0, overflow: "auto" }}>
            {e.selected ? (
              <WorkflowCanvas draftId={e.selected.workflow_id} graph={e.graph} dirty={e.dirty} readOnly={isMobile} onChange={e.setGraph} />
            ) : <div>{t("task.workflowEditor.selectOrCreate")}</div>}
          </div>
        </div>
      )}
    </div>
  );
}

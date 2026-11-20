import React, { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { useResponsive } from "../../hooks/useResponsive";
import { useBackLayer } from "../../hooks/useBackLayer";
import { panelButtonStyle, panelInputStyle, panelSurfaceStyle } from "../shell/PanelShell";
import { PromptEditor } from "../editor/PromptEditor";
import { PlusIcon } from "../action/composerIcons";
import { DeleteIcon, EditPencilIcon } from "./taskIcons";
import { WorkflowCanvas } from "./WorkflowCanvas";
import { WorkflowAuthoringPanel } from "./WorkflowAuthoringPanel";
import { useWorkflowAuthoring } from "./useWorkflowAuthoring";
import { useWorkflowAuthoringReview } from "./useWorkflowAuthoringReview";
import { useWorkflowDraftEditor } from "./useWorkflowDraftEditor";
import { workflowDraftDisplayName } from "../../shared/workflowModel";
import { authoringPanelExpanded, draftCollapseFrame, draftHandleFrame, draftPanelFrame } from "./workflowEditorLayout";
import { CloseIcon } from "./WorkflowNodeIcons";
import type { AgentStatus } from "../../shared/agentTypes";

// 两个弹窗（新建 / 编辑名称描述）共用同一个遮罩，单独拎出来省版面。
const dialogOverlayStyle: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  background: "rgba(0, 0, 0, 0.5)",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: 16,
  zIndex: 1000,
};

// 「工作流画布」那一行右侧的小按钮（保存 / 还原）：比别人小一号，不抢画布。
const canvasActionStyle: React.CSSProperties = { height: 22, padding: "0 8px", fontSize: 11 };

// 首行只放图标的小按钮：编辑名称、删除工作流。
const headerIconButtonStyle: React.CSSProperties = {
  padding: "2px 6px",
  minWidth: "unset",
  fontSize: 11,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  flexShrink: 0,
};

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
  // 手机上工作流列表是浮在画布上的抽屉：进编辑器先展开让人选工作流，选完自动收起，
  // 之后靠左边缘的悬浮把手再打开；点抽屉外面也收起（用户 2026-11-20 的第 5 条）。
  // 桌面恒为固定竖栏，这个状态不参与。
  const [draftPanelOpen, setDraftPanelOpen] = useState(true);
  const draftPanelRef = useRef<HTMLElement | null>(null);
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
    // 新建完同样让开画布：手机抽屉默认展开只是为了让人先选，选/建完就该收起来
    // （用户 2026-11-20 的第 5 条）。
    if (isMobile) setDraftPanelOpen(false);
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

  // 手机抽屉点外面就收起（用户 2026-11-20 的第 5 条）。用 pointerdown 而不是
  // click：手指按到画布上立刻收，不用等抬手，也不会和画布的拖拽抢事件。
  // 抽屉里的点击由 contains 判掉，选工作流那条路径自己也会收。
  useEffect(() => {
    if (!isMobile || !draftPanelOpen) return;
    const onPointerDown = (event: PointerEvent) => {
      const panel = draftPanelRef.current;
      if (panel && event.target instanceof Node && panel.contains(event.target)) return;
      setDraftPanelOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => document.removeEventListener("pointerdown", onPointerDown, true);
  }, [isMobile, draftPanelOpen]);

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
          style={dialogOverlayStyle}
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
          style={dialogOverlayStyle}
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

      {/* 首行：任务名 + 编辑/删除（居左）…… 关闭（居右）。手机上也是这一行，
          名字单行省略，按钮永远不换行（用户 2026-11-20 的第 8 条）。
          删除按钮从这里删掉当前选中的工作流 —— 列表里不再重复放一份。 */}
      <div style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
        <strong style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>
          {createMode
            ? t("task.workflowAuthoring.createTitle")
            : e.selected ? workflowDraftDisplayName(e.selected) : t("task.workflowEditor.title")}
        </strong>
        {e.dirty ? <span data-testid="workflow-draft-dirty" style={{ width: 8, height: 8, borderRadius: "50%", background: "var(--danger-color,#ef4444)", flexShrink: 0 }} /> : null}
        {!createMode && e.selected ? (
          <>
            <button type="button" data-testid="workflow-draft-edit-meta" aria-label={t("task.workflowEditor.editMeta")} title={t("task.workflowEditor.editMeta")} onClick={openMetaDialog} style={{ ...panelButtonStyle("secondary"), ...headerIconButtonStyle }}>
              <EditPencilIcon />
            </button>
            <button type="button" data-testid="workflow-draft-delete" data-workflow-id={e.selected.workflow_id} aria-label={t("task.workflowEditor.delete")} title={t("task.workflowEditor.delete")} disabled={!!e.deletingId || e.saving} onClick={() => void e.remove(e.selected!)} style={{ ...panelButtonStyle("secondary"), ...headerIconButtonStyle, color: "#dc2626" }}>
              <DeleteIcon />
            </button>
          </>
        ) : null}
        <span style={{ flex: 1 }} />
        {createMode ? (
          <button type="button" style={panelButtonStyle("secondary")} onClick={cancelCreate}>{t("task.workflowAuthoring.cancelCreate")}</button>
        ) : null}
        {/* 关闭用「×」图标（用户 2026-11-20），与编辑/删除同一种小图标按钮。 */}
        <button type="button" data-testid="workflow-editor-close" aria-label={t("task.workflowEditor.close")} title={t("task.workflowEditor.close")} onClick={close} style={{ ...panelButtonStyle("secondary"), ...headerIconButtonStyle }}>
          <CloseIcon size={14} />
        </button>
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
      {e.error ? <div role="alert" style={{ color: "var(--danger-color,#ef4444)", fontSize: 12 }}>{e.error}</div> : null}
      {!e.loading && (e.selected || createMode) ? (
        <WorkflowAuthoringPanel
          isMobile={isMobile}
          expanded={authoringExpanded}
          onToggle={() => setAuthoringOpen((open) => !open)}
          createMode={createMode}
          instruction={instruction}
          onInstructionChange={setInstruction}
          resetKey={createMode ? createIdRef.current : e.selectedId}
          onGenerate={generate}
          generating={authoring.state.phase === "generating"}
          dirty={e.dirty}
          authoring={authoring}
          review={review}
          onApplied={(result) => {
            if (result.draft) {
              setCreateMode(false);
              e.adoptDraft(result.draft);
            }
          }}
        />
      ) : null}
      {e.loading ? <div>{t("task.workflowEditor.loading")}</div> : (
        // 手机抽屉是 absolute 定位的（top/bottom 撑满这一列），所以这一列必须有高度：
        // 没选工作流时内容只有一行提示，容器会塌成十几像素，抽屉跟着变成一条缝。
        <div style={{ position: isMobile ? "relative" : undefined, display: "flex", flexDirection: isMobile ? "column" : "row", minHeight: isMobile ? "min(60vh, 520px)" : 0, flex: isMobile ? "none" : 1, gap: isMobile ? 8 : 12 }}>
          {/* 手机上工作流列表是浮在画布上方的左侧抽屉（竖向），收起后完全隐藏、
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
              // 把手在侧栏竖向居中（用户 2026-11-20 的第 5 条），拇指不用够顶部。
              style={{ ...panelButtonStyle("secondary"), ...draftHandleFrame() }}
            >
              ▸
            </button>
          ) : null}
          {/* 展开时的收起把手：贴在抽屉右缘、与左缘把手同一个竖向中点（不再是
              抽屉标题行里的一个小按钮）。它是抽屉的兄弟节点 —— 抽屉自己会滚动，
              放进去的绝对定位按钮会跟着列表滚走。 */}
          {isMobile && draftPanelOpen ? (
            <button
              type="button"
              data-testid="workflow-draft-panel-collapse"
              aria-label={t("task.workflowEditor.collapseDrafts")}
              aria-expanded
              title={t("task.workflowEditor.collapseDrafts")}
              onClick={() => setDraftPanelOpen(false)}
              style={{ ...panelButtonStyle("secondary"), ...draftCollapseFrame() }}
            >
              ◂
            </button>
          ) : null}
          <nav
            ref={draftPanelRef}
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
                      想读描述把鼠标停在卡上看 title；单行名字仍然省略号截断。
                      删除按钮不在这里 —— 它跟着选中的工作流住在首行（用户 2026-11-20
                      的第 6/8 条），列表只负责选。 */}
                  <button
                    type="button"
                    style={{ ...panelButtonStyle(isSelected ? "primary" : "secondary"), display: "block", textAlign: "left", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                    aria-current={isSelected}
                    onClick={() => { setCreateMode(false); if (isMobile) setDraftPanelOpen(false); void e.choose(d); }}
                    title={d.description ? `${displayName} (${d.revision}) — ${d.description}` : `${displayName} (${d.revision})`}
                  >
                    {displayName}
                  </button>
                </div>
              );
            })}
            {e.drafts.length === 0 ? <span style={{ flexShrink: 0 }}>{t("task.workflowEditor.empty")}</span> : null}
          </nav>
          <div style={{ flex: 1, minWidth: 0, minHeight: 0, overflow: isMobile ? "visible" : "auto" }}>
            {createMode ? null : e.selected ? (
              <WorkflowCanvas
                draftId={e.selected.workflow_id}
                graph={e.graph}
                dirty={e.dirty}
                onChange={e.setGraph}
                agents={agents}
                actions={
                  // 保存/还原来到「工作流画布」这一行的右侧（用户 2026-11-20 的第 7 条）。
                  // 保存即发布：没有单独的发布按钮，也没有版本概念。
                  <>
                    <button type="button" data-testid="workflow-draft-save" disabled={e.saving || !e.dirty}
                      style={{ ...panelButtonStyle("primary"), ...canvasActionStyle }} onClick={() => void e.save()}>
                      {e.saving ? t("task.workflowEditor.saving") : t("task.workflowEditor.save")}
                    </button>
                    <button type="button" data-testid="workflow-draft-revert" disabled={e.saving || !e.dirty}
                      style={{ ...panelButtonStyle("secondary"), ...canvasActionStyle }} onClick={e.revert}>
                      {t("task.workflowEditor.revert")}
                    </button>
                  </>
                }
              />
            ) : <div>{t("task.workflowEditor.selectOrCreate")}</div>}
          </div>
        </div>
      )}
    </div>
  );
}
import React from "react";
import { useI18n } from "../i18n";
import { useResponsive } from "../app/appMisc";
import { TASK_TEMPLATE_ALL_FILTER } from "../app/appStorage";
import { useRefreshSpin } from "../hooks";
import type { useTaskTemplates } from "../app/useTaskTemplates";
import type { KanbanTask, TaskTemplate } from "../services/tasks";
import type { WorkspaceBoard as WorkspaceBoardData } from "../app/useWorkspaceBoard";
import type { WorkspaceBoardFilter } from "../app/appStorage";
import type { SessionItem } from "../app/appSession";
import { InlineTokenText } from "./InlineTokenText";
import { TaskCardRows } from "./TaskCardRows";
import { WorkspaceBoard } from "./workspace/WorkspaceBoard";
import {
  DeleteIcon,
  EditPencilIcon,
  HorizontalDotsIcon,
  PlusSmallIcon,
  SyncIcon,
  TaskExpandIcon,
  TaskGroupChevronIcon,
  hexToRgbaApp,
  taskCardIconButtonStyle,
  taskCardSurfaceStyle,
  taskTemplateMenuItemStyle,
} from "../app/taskIcons";

/** 看板列（四块：未开始 / 执行中 / 待审核 / 已结束）。App 侧构造，本视图只渲染。 */
export type KanbanStageColumn = {
  index: number;
  name: string;
  role: "user" | "agent";
  tasks: KanbanTask[];
  groups?: Array<{ key: string; name: string; tone: "success" | "danger" | "muted"; tasks: KanbanTask[] }>;
};

/** 任务会话报错弹窗内容。App 持有该状态（别处也会打开它），类型放这里共用。 */
export type TaskSessionErrorDialog = { title: string; message: string; details: string[] };

type MoveKanbanAction = "next" | "run-now" | "pause" | "resume" | "complete" | "cancel";

/**
 * 任务面板：无项目时是跨项目工作台（WorkspaceBoard），有项目时是项目内四块看板。
 *
 * 纯视图——数据与回调全由 App 传入。折叠/选中这类"看起来是本地的"状态其实 App 也要用
 * （详情面板、WS 回包），所以照样由 App 持有，组件内不新增状态。
 * 任务模板那一组（refs / 开合 / 模板表 / setter / 回调）整体走 templateControls 一个分组 prop：
 * 它们正好是 useTaskTemplates 的返回子集，用 Pick<ReturnType<…>> 取类型，上游改名会直接编译报错。
 */
export function TaskBoardView({
  workspaceOpen,
  currentRootId,
  currentRootIdRef,
  workspaceBoard,
  workspaceFilter,
  onWorkspaceFilterChange,
  workspaceCollapsedKeys,
  onToggleWorkspaceProject,
  onRefreshWorkspaceBoard,
  managedRootIds,
  getRootDisplayName,
  openWorkspaceProject,
  openWorkspaceTaskDetail,
  handleWorkspaceCreateTask,
  setSelectedKanbanTaskId,
  kanbanTasksLoading,
  kanbanStageColumns,
  selectedKanbanTaskId,
  expandedTaskInputIds,
  setExpandedTaskInputIds,
  collapsedTaskCompletionGroups,
  setCollapsedTaskCompletionGroups,
  collapsedKanbanColumns,
  setCollapsedKanbanColumns,
  taskFirstInputById,
  taskSessionKeysById,
  sessionByKey,
  getDisplayNodeColor,
  handleSelectKanbanTask,
  handleMoveKanbanTask,
  openTaskCreateDialog,
  handleTaskSessionDrawerOpen,
  setTaskSessionErrorDialog,
  loadKanbanTasks,
  templateControls,
}: {
  workspaceOpen: boolean;
  currentRootId: string | null;
  currentRootIdRef: React.MutableRefObject<string | null>;
  workspaceBoard: WorkspaceBoardData;
  workspaceFilter: WorkspaceBoardFilter;
  onWorkspaceFilterChange: (filter: WorkspaceBoardFilter) => void;
  workspaceCollapsedKeys: Set<string>;
  onToggleWorkspaceProject: (key: string) => void;
  onRefreshWorkspaceBoard: () => void;
  managedRootIds: string[];
  getRootDisplayName: (rootId: string | null | undefined) => string;
  openWorkspaceProject: (rootId: string) => Promise<void>;
  openWorkspaceTaskDetail: (item: { root_id: string; nodeId: string; task: KanbanTask }) => Promise<void>;
  handleWorkspaceCreateTask: (rootId: string, nodeId: string, template: TaskTemplate) => void;
  setSelectedKanbanTaskId: React.Dispatch<React.SetStateAction<string>>;
  kanbanTasksLoading: boolean;
  kanbanStageColumns: KanbanStageColumn[];
  selectedKanbanTaskId: string;
  expandedTaskInputIds: Set<string>;
  setExpandedTaskInputIds: React.Dispatch<React.SetStateAction<Set<string>>>;
  collapsedTaskCompletionGroups: Set<string>;
  setCollapsedTaskCompletionGroups: React.Dispatch<React.SetStateAction<Set<string>>>;
  collapsedKanbanColumns: Set<string>;
  setCollapsedKanbanColumns: React.Dispatch<React.SetStateAction<Set<string>>>;
  taskFirstInputById: Record<string, string>;
  taskSessionKeysById: Record<string, string[]>;
  sessionByKey: Record<string, SessionItem>;
  getDisplayNodeColor: (rootId: string) => string | null;
  handleSelectKanbanTask: (task: KanbanTask) => void;
  handleMoveKanbanTask: (task: KanbanTask, action: MoveKanbanAction) => Promise<void>;
  openTaskCreateDialog: (template: TaskTemplate | null, targetRootId?: string) => void;
  handleTaskSessionDrawerOpen: (sessionKey: string, rootOverride?: string | null, taskId?: string) => void;
  setTaskSessionErrorDialog: React.Dispatch<React.SetStateAction<TaskSessionErrorDialog | null>>;
  loadKanbanTasks: (rootId?: string | null, force?: boolean) => Promise<void>;
  templateControls: Pick<
    ReturnType<typeof useTaskTemplates>,
    | "taskTemplateActionMenuRef"
    | "taskTemplateActionMenuOpen"
    | "setTaskTemplateActionMenuOpen"
    | "taskCreateTemplateMenuRef"
    | "taskCreateTemplateMenuOpen"
    | "setTaskCreateTemplateMenuOpen"
    | "taskTemplates"
    | "taskTemplateFilter"
    | "setTaskTemplateFilter"
    | "openTaskTemplateEditor"
    | "handleDeleteTaskTemplate"
  >;
}) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const kanbanRefreshSpin = useRefreshSpin(() => loadKanbanTasks(currentRootId));
  const {
    taskTemplateActionMenuRef,
    taskTemplateActionMenuOpen,
    setTaskTemplateActionMenuOpen,
    taskCreateTemplateMenuRef,
    taskCreateTemplateMenuOpen,
    setTaskCreateTemplateMenuOpen,
    taskTemplates,
    taskTemplateFilter,
    setTaskTemplateFilter,
    openTaskTemplateEditor,
    handleDeleteTaskTemplate,
  } = templateControls;
  const isAllTaskTemplateFilter = taskTemplateFilter === TASK_TEMPLATE_ALL_FILTER;
  const selectedTaskTemplateForFilter = isAllTaskTemplateFilter ? null : taskTemplates.find((template) => template.id === taskTemplateFilter) || null;

  const workspacePanel = (
    <WorkspaceBoard
      board={workspaceBoard}
      filter={workspaceFilter}
      onFilterChange={onWorkspaceFilterChange}
      collapsedKeys={workspaceCollapsedKeys}
      onToggleProject={onToggleWorkspaceProject}
      getNodeColor={getDisplayNodeColor}
      onOpenProject={(rootId) => { void openWorkspaceProject(rootId); }}
      onOpenTask={(item) => { void openWorkspaceTaskDetail(item); }}
      onMoveTask={(item, action) => { void handleMoveKanbanTask(item.task, action); }}
      onOpenSession={(sessionKey, rootId, taskId) => {
        handleTaskSessionDrawerOpen(sessionKey, rootId, taskId);
      }}
      onShowSessionError={setTaskSessionErrorDialog}
      taskSessionKeysById={taskSessionKeysById}
      sessionByKey={sessionByKey}
      templates={templateControls.taskTemplates}
      onCreateTask={handleWorkspaceCreateTask}
      onRefresh={onRefreshWorkspaceBoard}
    />
  );

  // 有项目就看项目内四块看板；没有项目才由跨项目工作台接管；都没有就什么都不渲染。
  if (workspaceOpen) return workspacePanel;
  if (!currentRootId) return null;

  return (
    <div
      data-onboarding="task-board"
      style={{
        maxHeight: "calc(100dvh - 92px)",
        overflow: "visible",
        display: "flex",
        flexDirection: "column",
        minWidth: 0,
      }}
    >
      <div
        style={{
          display: "flex",
        alignItems: "center",
          justifyContent: "space-between",
          gap: 0,
          padding: "0 0 8px",
                    flexShrink: 0,
        }}
      >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "6px",
          minWidth: 0,
          flex: 1,
        }}
      >
        <div
          role="tablist"
          data-onboarding="task-templates"
          aria-label={t("task.templates")}
          style={{
            display: "flex",
            alignItems: "center",
            gap: "2px",
            overflowX: "auto",
            padding: "3px",
            borderRadius: "10px",
            border: "1px solid rgba(100, 116, 139, 0.36)",
            background: "rgba(148, 163, 184, 0.10)",
            minWidth: 0,
            scrollbarWidth: "none",
          }}
        >
          {(() => {
            const active = isAllTaskTemplateFilter;
            const kanbanAllBg = getDisplayNodeColor(String(currentRootId || "")) || "var(--accent-color)";
            return (
              <button
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => {
                  setTaskTemplateFilter(TASK_TEMPLATE_ALL_FILTER);
                  setTaskTemplateActionMenuOpen(false);
                }}
                style={{
                  border: "none",
                  borderRadius: "6px",
                  background: active ? kanbanAllBg : "transparent",
                  color: active ? "#fff" : "var(--text-secondary)",
                  padding: "3px 7px",
                  fontSize: "11px",
                  fontWeight: 700,
                  lineHeight: "14px",
                  cursor: "pointer",
                  whiteSpace: "nowrap",
                  boxShadow: active ? `0 1px 3px ${hexToRgbaApp(kanbanAllBg, 0.28)}` : "none",
                }}
              >
                {t("task.all")}
              </button>
            );
          })()}
          {taskTemplates.length > 0 ? (
            <span
              aria-hidden="true"
              style={{
                width: "1px",
                height: "16px",
                background: "rgba(100, 116, 139, 0.32)",
                margin: "0 1px",
                    flexShrink: 0,
              }}
            />
          ) : null}
          {taskTemplates.map((template, index) => {
            const templateId = template.id || "";
            const active = selectedTaskTemplateForFilter?.id === templateId;
            return (
              <React.Fragment key={templateId || template.name}>
                {index > 0 ? (
                  <span
                    aria-hidden="true"
                    style={{
                      width: "1px",
                      height: "16px",
                      background: "rgba(100, 116, 139, 0.32)",
                      margin: "0 1px",
                    flexShrink: 0,
                    }}
                  />
                ) : null}
                <button
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => {
                    setTaskTemplateFilter(templateId);
                    setTaskTemplateActionMenuOpen(false);
                  }}
                  style={(() => {
                    const bg = getDisplayNodeColor(String(currentRootId || "")) || "var(--accent-color)";
                    return {
                      border: "none",
                      borderRadius: "6px",
                      background: active ? bg : "transparent",
                      color: active ? "#fff" : "var(--text-secondary)",
                      padding: "3px 7px",
                      fontSize: "11px",
                      fontWeight: 700,
                      lineHeight: "14px",
                      cursor: "pointer",
                      whiteSpace: "nowrap",
                      boxShadow: active ? `0 1px 3px ${hexToRgbaApp(bg, 0.28)}` : "none",
                    };
                  })()}
              >
                <span>{template.name || t("task.unnamedTemplate")}</span>
              </button>
              </React.Fragment>
            );
          })}
        </div>
        <div ref={taskTemplateActionMenuRef} style={{ position: "relative", flexShrink: 0 }}>
          <button
            type="button"
            data-onboarding="task-template-menu"
            aria-label={t("task.templateMenu")}
            title={t("task.templateMenu")}
            onClick={() => setTaskTemplateActionMenuOpen((open) => !open)}
            style={{
              width: "28px",
              height: "28px",
              borderRadius: "8px",
              border: "none",
              background: taskTemplateActionMenuOpen ? "rgba(0, 0, 0, 0.06)" : "transparent",
              color: "var(--text-secondary)",
              opacity: 1,
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              cursor: "pointer",
              padding: 0,
            }}
          >
            <HorizontalDotsIcon />
          </button>
          {taskTemplateActionMenuOpen ? (
            <div
              style={{
                position: "absolute",
                top: "calc(100% + 6px)",
                left: "50%",
                transform: "translateX(-50%)",
                minWidth: "160px",
                padding: "6px",
                borderRadius: "10px",
                border: "1px solid var(--border-color)",
                background: "var(--menu-bg)",
                boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
                zIndex: 40,
              }}
            >
              <button
                type="button"
                onClick={() => {
                  setTaskTemplateActionMenuOpen(false);
                  openTaskTemplateEditor(null);
                }}
                style={taskTemplateMenuItemStyle()}
              >
                <PlusSmallIcon />
                <span>{t("task.createTemplate")}</span>
              </button>
              <button
                type="button"
                disabled={!selectedTaskTemplateForFilter}
                onClick={() => {
                  if (!selectedTaskTemplateForFilter) return;
                  setTaskTemplateActionMenuOpen(false);
                  openTaskTemplateEditor(selectedTaskTemplateForFilter);
                }}
                style={taskTemplateMenuItemStyle(!selectedTaskTemplateForFilter)}
              >
                <EditPencilIcon />
                <span>{t("task.editTemplate")}</span>
              </button>
              <button
                type="button"
                disabled={!selectedTaskTemplateForFilter}
                title={!selectedTaskTemplateForFilter ? t("task.selectTemplate") : t("task.deleteTemplate")}
                onClick={() => {
                  if (!selectedTaskTemplateForFilter) return;
                  setTaskTemplateActionMenuOpen(false);
                  void handleDeleteTaskTemplate(selectedTaskTemplateForFilter);
                }}
                style={taskTemplateMenuItemStyle(!selectedTaskTemplateForFilter)}
              >
                <DeleteIcon />
                <span>{t("task.deleteTemplate")}</span>
              </button>
              <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 2px" }} />
            </div>
          ) : null}
        </div>
      </div>
      <button
        type="button"
        data-onboarding="task-refresh"
        title={t("task.refresh")}
        aria-label={t("task.refresh")}
        onClick={() => void kanbanRefreshSpin.handleClick()}
        onMouseDown={() => kanbanRefreshSpin.setPressed(true)}
        onMouseUp={() => kanbanRefreshSpin.setPressed(false)}
        onMouseLeave={() => kanbanRefreshSpin.setPressed(false)}
        style={{
          width: "22px",
          height: "28px",
          borderRadius: "8px",
          border: "none",
          background: "transparent",
          color: "var(--text-color)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "flex-end",
          cursor: "pointer",
                    flexShrink: 0,
          padding: 0,
        }}
      >
        <span
          data-task-refresh-visual
          style={{
            width: "18px",
            height: "28px",
            borderRadius: "8px",
            background: kanbanRefreshSpin.pressed || kanbanRefreshSpin.refreshing ? "rgba(0, 0, 0, 0.06)" : "transparent",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          <SyncIcon
            style={kanbanRefreshSpin.refreshing ? { animation: "mindfs-update-spin 0.8s linear infinite" } : undefined}
          />
        </span>
      </button>
      <div ref={taskCreateTemplateMenuRef} style={{ position: "relative", flexShrink: 0 }}>
        <button
          type="button"
          data-onboarding="task-create"
          title={t("task.create")}
          aria-label={t("task.create")}
          disabled={!isAllTaskTemplateFilter && !selectedTaskTemplateForFilter}
          onClick={() => {
            if (isAllTaskTemplateFilter) {
              setTaskTemplateActionMenuOpen(false);
              setTaskCreateTemplateMenuOpen((open) => !open);
              return;
            }
            openTaskCreateDialog(selectedTaskTemplateForFilter);
          }}
          style={{
            width: "28px",
            height: "28px",
            borderRadius: "8px",
            border: "none",
            background: taskCreateTemplateMenuOpen ? "rgba(0, 0, 0, 0.06)" : "transparent",
            color: isAllTaskTemplateFilter || selectedTaskTemplateForFilter ? "var(--text-color)" : "var(--muted-text)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            cursor: isAllTaskTemplateFilter || selectedTaskTemplateForFilter ? "pointer" : "not-allowed",
                    flexShrink: 0,
            padding: 0,
            opacity: isAllTaskTemplateFilter || selectedTaskTemplateForFilter ? 1 : 0.55,
          }}
        >
          <PlusSmallIcon />
        </button>
        {taskCreateTemplateMenuOpen ? (
          <div
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              right: 0,
              minWidth: "136px",
              maxWidth: "220px",
              maxHeight: "260px",
              overflowY: "auto",
              padding: "5px",
              borderRadius: "10px",
              border: "1px solid var(--border-color)",
              background: "var(--menu-bg)",
              boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
              zIndex: 40,
            }}
          >
            {taskTemplates.length === 0 ? (
              <div style={{ padding: "8px", fontSize: "12px", color: "var(--text-secondary)", whiteSpace: "nowrap" }}>{t("task.noTemplates")}</div>
            ) : taskTemplates.map((template) => (
              <button
                key={template.id || template.name}
                type="button"
                onClick={() => {
                  setTaskCreateTemplateMenuOpen(false);
                  openTaskCreateDialog(template);
                }}
                style={{
                  width: "100%",
                  minHeight: "28px",
                  border: "none",
                  borderRadius: "7px",
                  background: "transparent",
                  color: "var(--text-primary)",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "flex-start",
                  padding: "5px 8px",
                  fontSize: "12px",
                  fontWeight: 700,
                  cursor: "pointer",
                  textAlign: "left",
                }}
              >
                <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{template.name || t("task.unnamedTemplate")}</span>
              </button>
            ))}
          </div>
        ) : null}
      </div>
    </div>
    {kanbanTasksLoading ? (
      <div style={{ padding: "12px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.loading")}</div>
    ) : !isAllTaskTemplateFilter && !selectedTaskTemplateForFilter ? (
      <div style={{ padding: "12px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.createTemplateFirst")}</div>
    ) : (
        <div style={{ overflowX: isMobile ? "hidden" : "auto", overflowY: "hidden", padding: "0 0 12px 1px", minHeight: 0 }}>
          <div
            style={{
              display: "grid",
              // 移动端横滑空间浪费：列改为换行（每行两列），列内上下滑看卡片。
              gridAutoFlow: isMobile ? "row" : "column",
              gridTemplateColumns: isMobile ? "repeat(2, 1fr)" : undefined,
              gridAutoColumns: isMobile ? undefined : "minmax(220px, 1fr)",
              gridAutoRows: isMobile ? "36dvh" : undefined,
              gap: "6px",
              minWidth: isMobile ? undefined : `${Math.max(kanbanStageColumns.length, 1) * 220}px`,
              alignItems: "start",
            }}
          >
            {kanbanStageColumns.map((column) => {
              const columnCollapsed = collapsedKanbanColumns.has(String(column.index));
              const taskSections = "groups" in column && Array.isArray(column.groups) && column.groups.length > 0
                ? column.groups
                : [{ key: "tasks", name: "", tone: "default" as const, tasks: column.tasks }];
              return (
              <section
                key={column.index}
              style={{
                    border: "1px solid var(--border-color)",
                  borderRadius: "8px",
                  background: "rgba(148, 163, 184, 0.06)",
                  overflow: "hidden",
                  display: "flex",
                  flexDirection: "column",
                  minHeight: 0,
                  height: isMobile ? (columnCollapsed ? undefined : "36dvh") : undefined,
                maxHeight: isMobile ? undefined : "calc(100dvh - 96px)",
                }}
              >
              <div
                style={{
                  minHeight: "34px",
                  borderBottom: "1px solid var(--border-color)",
                  padding: "7px 9px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                    gap: "8px",
                    flexShrink: 0,
                  cursor: "pointer",
                }}
                onClick={() => {
                  const key = String(column.index);
                  setCollapsedKanbanColumns((prev) => {
                    const next = new Set(prev);
                    if (next.has(key)) next.delete(key); else next.add(key);
                    return next;
                  });
                }}
              >
                <div style={{ minWidth: 0, display: "flex", alignItems: "center", gap: "6px" }}>
                  <TaskGroupChevronIcon collapsed={columnCollapsed} />
                  <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: "12px", fontWeight: 800, color: "var(--text-color)" }}>
                    {column.name}
                  </span>
                </div>
                <span style={{ fontSize: "11px", fontWeight: 800, color: "var(--text-secondary)" }}>{column.tasks.length}</span>
              </div>
                {columnCollapsed ? null : (
                <div style={{ padding: "8px", display: "flex", flexDirection: "column", gap: "8px", overflowY: "auto", minHeight: 0 }}>
                  {column.tasks.length === 0 ? (
                    <div style={{ padding: "10px 4px", fontSize: "12px", color: "var(--text-secondary)", textAlign: "center" }}>{t("task.empty")}</div>
                      ) : taskSections.map((section) => {
                    const sectionCollapsed = Boolean(section.name && collapsedTaskCompletionGroups.has(section.key));
                    const sectionColor = section.tone === "danger" ? "#dc2626" : section.tone === "success" ? "#16a34a" : "var(--text-secondary)";
                    return (
                    <React.Fragment key={section.key}>
                      {section.name ? (
                        <button
                          type="button"
                          onClick={() => {
                            setCollapsedTaskCompletionGroups((prev) => {
                              const next = new Set(prev);
                              if (next.has(section.key)) {
                                next.delete(section.key);
                              } else {
                                next.add(section.key);
                              }
                              return next;
                            });
                          }}
                          style={{
                            marginTop: "2px",
                            padding: "2px 2px 0",
                            display: "flex",
                            alignItems: "center",
                            justifyContent: "space-between",
                            gap: "8px",
                            width: "100%",
                            border: "none",
                            background: "transparent",
                            color: sectionColor,
                            fontSize: "11px",
                            fontWeight: 800,
                            cursor: "pointer",
                          }}
                        >
                          <span style={{ display: "inline-flex", alignItems: "center", gap: "4px", minWidth: 0 }}>
                            <TaskGroupChevronIcon collapsed={sectionCollapsed} />
                            <span>{section.name}</span>
                          </span>
                          <span>{section.tasks.length}</span>
                        </button>
                      ) : null}
                      {!sectionCollapsed ? section.tasks.map((task) => {
                  const firstInput = taskFirstInputById[task.id] || "";
                  const taskSessionKeys = taskSessionKeysById[task.id]?.length
                    ? taskSessionKeysById[task.id]
                    : task.main_session_key
                      ? [task.main_session_key]
                      : [];
                  // 正文要不要折叠由看板自己决定 —— 正文段在 TaskCardRows 的 children 里，不在共享组件内。
                  const inputExpanded = expandedTaskInputIds.has(task.id);
                  // 正文超过 3 行（或 120 字）才需要展开开关 —— 它只对正文生效
                  const inputNeedsToggle = firstInput.length > 120 || firstInput.split(/\r?\n/).length > 3;
                    const taskStageName = task.current_stage_name || (task.current_stage_index >= 0 ? t("task.stageLabel", { index: task.current_stage_index + 1 }) : "");
                    // 列语义对两种筛选态一视同仁：阶段名只在"执行中"列、状态只在"已结束"列出现。
                    // 以前子看板走另一套（恒显示阶段），于是同一张卡在两边信息量不同。
                    const showStageName = column.name === t("task.column.running") && Boolean(taskStageName);
                    const showTaskStatus = column.name === t("task.column.ended");
                    const taskSelected = selectedKanbanTaskId === task.id;
                    return (
                      <article
                        key={task.id}
                        onClick={() => handleSelectKanbanTask(task)}
                        style={{ ...taskCardSurfaceStyle(taskSelected), padding: "8px" }}
                      >
                      <TaskCardRows
                        task={task}
                        templateNameFallback={selectedTaskTemplateForFilter?.name || ""}
                        sessionKeys={taskSessionKeys}
                        sessionByKey={sessionByKey}
                        getNodeColor={getDisplayNodeColor}
                        onOpenSession={(sessionKey, rootId, taskId) => {
                          handleTaskSessionDrawerOpen(sessionKey, rootId || currentRootIdRef.current, taskId);
                        }}
                        onMove={(cardTask, action) => { void handleMoveKanbanTask(cardTask, action); }}
                        onShowSessionError={(payload) => { setTaskSessionErrorDialog(payload); }}
                        stageName={showStageName ? taskStageName : ""}
                        showStatus={showTaskStatus}
                        hideSessionError={showTaskStatus && task.status === "fail"}
                      >
                        <div
                          style={{
                            marginTop: "5px",
                            color: firstInput ? "var(--text-color)" : "var(--text-secondary)",
                            fontSize: "12px",
                            lineHeight: "18px",
                            fontWeight: firstInput ? 700 : 500,
                          }}
                        >
                          <div
                            style={{
                              whiteSpace: "pre-wrap",
                              wordBreak: "break-word",
                              ...(!inputExpanded
                                ? {
                                    display: "-webkit-box",
                                    WebkitLineClamp: 3,
                                    WebkitBoxOrient: "vertical",
                                    overflow: "hidden",
                                  }
                                : {}),
                            }}
                          >
                            {firstInput ? <InlineTokenText content={firstInput} /> : <span>{t("task.noInput")}</span>}
                          </div>
                        </div>
                        {/* 展开/收起只管正文这一段，所以按钮跟着正文住在看板这一侧的 children 里
                            （共享的 TaskCardRows 不碰正文，也就不该有它的开关）。 */}
                        {inputNeedsToggle ? (
                          <div style={{ display: "flex", justifyContent: "flex-start", marginTop: "3px" }}>
                            <button
                              type="button"
                              title={inputExpanded ? t("common.collapse") : t("common.expand")}
                              aria-label={inputExpanded ? t("task.collapseContent") : t("task.expandContent")}
                              onClick={(event) => {
                                event.stopPropagation();
                                setExpandedTaskInputIds((prev) => {
                                  const next = new Set(prev);
                                  if (next.has(task.id)) {
                                    next.delete(task.id);
                                  } else {
                                    next.add(task.id);
                                  }
                                  return next;
                                });
                              }}
                              style={taskCardIconButtonStyle()}
                            >
                              <TaskExpandIcon collapsed={!inputExpanded} />
                            </button>
                          </div>
                        ) : null}
                      </TaskCardRows>
                      </article>
                    );
                  }) : null}
                    </React.Fragment>
                    );
                  })}
                </div>
                )}
              </section>
              );
            })}
        </div>
      </div>
    )}
  </div>
  );
}

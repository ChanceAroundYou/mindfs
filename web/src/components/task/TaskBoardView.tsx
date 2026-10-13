import React from "react";
import { useI18n } from "../../i18n/index";
import { useResponsive } from "../../hooks/useResponsive";
import { TASK_TEMPLATE_ALL_FILTER } from "../../shared/taskTemplateConstants";
import type { KanbanTask, TaskTemplate } from "../../shared/taskTypes";
import type { WorkspaceBoard as WorkspaceBoardData, WorkspaceBoardFilter } from "../../shared/workspaceBoardTypes";
import type { SessionPresentationSummary } from "../../shared/sessionPresentationTypes";
import type { KanbanStageColumn, MoveKanbanAction, TaskSessionErrorDialog, TaskTemplateControls } from "./taskBoardTypes";
export type { KanbanStageColumn, TaskSessionErrorDialog } from "./taskBoardTypes";
import { InlineTokenText } from "../common/InlineTokenText";
import { FloatingScroll } from "../common/FloatingScroll";
import { TaskCardRows } from "./TaskCardRows";
import { TaskBoardToolbar } from "./TaskBoardToolbar";
import { WorkspaceBoard } from "../workspace/WorkspaceBoard";
import {
  TaskExpandIcon,
  TaskGroupChevronIcon,
  taskCardIconButtonStyle,
  taskCardSurfaceStyle,
  taskTemplateMenuItemStyle,
} from "../../app/taskIcons";

/**
 * 任务面板：无项目时是跨项目工作台（WorkspaceBoard），有项目时是项目内四块看板。
 *
 * 纯视图——数据与回调全由 App 传入。折叠/选中这类"看起来是本地的"状态其实 App 也要用
 * （详情面板、WS 回包），所以照样由 App 持有，组件内不新增状态。
 * 任务模板状态以 templateControls 整组传递；其 view contract 与 app hook 解耦，定义在 taskBoardTypes。
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
  sessionByKey: Record<string, SessionPresentationSummary>;
  getDisplayNodeColor: (rootId: string) => string | null;
  handleSelectKanbanTask: (task: KanbanTask) => void;
  handleMoveKanbanTask: (task: KanbanTask, action: MoveKanbanAction) => Promise<void>;
  openTaskCreateDialog: (template: TaskTemplate | null, targetRootId?: string) => void;
  handleTaskSessionDrawerOpen: (sessionKey: string, rootOverride?: string | null, taskId?: string) => void;
  setTaskSessionErrorDialog: React.Dispatch<React.SetStateAction<TaskSessionErrorDialog | null>>;
  loadKanbanTasks: (rootId?: string | null, force?: boolean) => Promise<void>;
  templateControls: TaskTemplateControls;
}) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const { taskTemplates, taskTemplateFilter } = templateControls;
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
        /* 铺满：以前这里写死 maxHeight: calc(100dvh - 92px)，而那 92px 猜的是
           ActionBar 高度 + 安全区，跟真实值对不上，内容一少底部就空一大截。
           高度交给 flex 链（DefaultListView 的滚动容器已经撑满了），
           那套 dvh 魔法数就此退休。 */
        flex: 1,
        minHeight: 0,
        overflow: "visible",
        display: "flex",
        flexDirection: "column",
        minWidth: 0,
      }}
    >
      <TaskBoardToolbar
        currentRootId={currentRootId}
        isAllTaskTemplateFilter={isAllTaskTemplateFilter}
        selectedTaskTemplateForFilter={selectedTaskTemplateForFilter}
        templateControls={templateControls}
        getDisplayNodeColor={getDisplayNodeColor}
        onRefresh={() => loadKanbanTasks(currentRootId)}
        openTaskCreateDialog={openTaskCreateDialog}
      />
    {kanbanTasksLoading ? (
      <div style={{ padding: "12px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.loading")}</div>
    ) : !isAllTaskTemplateFilter && !selectedTaskTemplateForFilter ? (
      <div style={{ padding: "12px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.createTemplateFirst")}</div>
    ) : (
        <div style={{ overflowX: isMobile ? "hidden" : "auto", overflowY: "hidden", padding: "0 0 12px 1px", minHeight: 0, flex: 1, minWidth: 0 }}>
          <div
            style={{
              display: "grid",
              // 百分比行高要有个**确定**高度才能解析。这一层拿得到值，是因为 DefaultListView
              // 的 topContent 包裹层声明了 height:"100%" + display:flex/column，
              // 本组件根因此是个高度已定的 flex item，网格容器 flex:1 又是已定的 flex item。
              // 链条上任何一环退回 auto，下面的 50% 就解析不出来、行高变内容高度 —— 老问题复发。
              height: "100%",
              // 移动端横滑空间浪费：列改为换行（每行两列），列内上下滑看卡片。
              gridAutoFlow: isMobile ? "row" : "column",
              // minmax(0,1fr) 而不是 1fr：默认的 auto 下限会被卡片内容顶宽，窄屏挤成一条。
              gridTemplateColumns: isMobile ? "repeat(2, minmax(0, 1fr))" : undefined,
              gridAutoColumns: isMobile ? undefined : "minmax(220px, 1fr)",
              // 行高：桌面四块同排，撑满即可；移动端两行必须**封顶**。
              // 这里踩过坑：写 minmax(0,1fr) 时父容器高度 auto、无上限，1fr 退化成
              // 「视口的一半」—— 四块两行直接撑到 2 个屏。所以移动端按 50% 切两行、
              // 各扣掉半个 gap，总高恰好等于可用高度，永远不外溢。
              gridAutoRows: isMobile ? "calc(50% - 3px)" : "100%",
              gap: "6px",
              minWidth: isMobile ? undefined : `${Math.max(kanbanStageColumns.length, 1) * 220}px`,
              // 列要撑满行高（alignItems 默认的 stretch）。以前写的是 "start"，
              // 那是配合 36dvh 定高用的；行高交给网格后必须放开，
              // 否则列退回内容高度，底部空带原封不动地回来。
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
                  // 列框（border/圆角/浅灰底）已移除（2026-10-07）：框占 2px 宽、
                  // 列表 padding 又占 16px，卡片被挤窄。去掉后卡片宽约 6px，
                  // 与工作台网格节奏一致。列的边界靠列头分隔线 + 网格 gap 表达。
                  display: "flex",
                  flexDirection: "column",
                  minHeight: 0,
                  // 高度由网格行（minmax(0,1fr)）给满，列内列表容器自己滚。
                  // 移动端行高同样由网格给；折叠的列按内容收缩，不拉成空壳。
                  height: columnCollapsed ? "auto" : "100%",
                }}
              >
              <div
                style={{
                  minHeight: "34px",
                  background: "rgba(148, 163, 184, 0.08)",
                  borderRadius: "6px",
                  borderBottom: "1px solid var(--border-color)",
                  padding: "7px 9px",
                  marginBottom: "6px",
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
                <FloatingScroll style={{ padding: "0", display: "flex", flexDirection: "column", gap: "6px" }}>
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
                    // 列语义对两种筛选态一视同仁：阶段名只在"执行中"列、状态在「已结束」
                    // 与「待审核」列出现。以前子看板走另一套（恒显示阶段），于是同一张卡在两边
                    // 信息量不同；「待审核」列补状态文字（2026-10-07 用户要求）后与工作台卡
                    // 同一渲染路径 —— 都是 TaskCardRows 的 showStatus，位置在名字后、worktree 前。
                    const showStageName = column.name === t("task.column.running") && Boolean(taskStageName);
                    const showTaskStatus = column.name === t("task.column.ended")
                      || column.name === t("task.column.waitingUser");
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
                </FloatingScroll>
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

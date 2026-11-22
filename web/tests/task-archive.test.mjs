// 看板归档（#86）：终态任务可以「归档」——只是任务自己的标记，卡片从工作台隐藏、
// 留在看板「已结束 → 归档」子组，级联到会话时同样只打标记、不删任何后代。
//
// 这里钉两件事，都跑组件/hook 的真实源码：
//   1. 「已结束」列的分组：完成 / 取消 不再包含已归档任务，归档是独立第三组，
//      三组之和必须等于列头数字（否则列头与内容对不上）。
//   2. 卡片上的键：未归档终态 = 回到待处理 + 归档 + 删除；已归档 = 取消归档 + 删除。

import assert from "node:assert/strict";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { useAppKanbanColumns } from "../src/app/useAppKanbanColumns.ts";
import { TaskCardRows } from "../src/components/task/TaskCardRows.tsx";
import { TaskBoardView } from "../src/components/task/TaskBoardView.tsx";

const terminalTask = (extra = {}) => ({
  id: "task-1",
  root_id: "root-1",
  task_template_id: "",
  task_template_name: "",
  status: "success",
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  ...extra,
});

const renderCard = (task) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(TaskCardRows, {
        task,
        sessionKeys: [],
        sessionByKey: {},
        getNodeColor: () => null,
        onOpenSession: () => {},
        onMove: () => {},
        onShowSessionError: () => {},
      }),
    ),
  );

test("ended column splits archived tasks into their own third group", () => {
  const tasks = [
    terminalTask({ id: "done" }),
    terminalTask({ id: "failed", status: "fail" }),
    terminalTask({ id: "cancelled", status: "cancelled" }),
    terminalTask({ id: "archived-success", archived_at: "2026-11-18T00:00:00Z" }),
    terminalTask({ id: "archived-cancelled", status: "cancelled", archived_at: "2026-11-18T00:00:00Z" }),
  ];
  const { kanbanColumns } = useAppKanbanColumns({ kanbanTasks: tasks, t: (key) => key });
  const ended = kanbanColumns.find((column) => column.index === 3);
  assert.ok(ended, "the ended column must exist");
  const groups = Object.fromEntries((ended.groups || []).map((group) => [group.key, group.tasks.map((task) => task.id)]));
  assert.deepEqual(groups.success, ["done"], "archived success must leave the 完成 group");
  assert.deepEqual(groups.cancelled?.sort(), ["cancelled", "failed"], "archived cancelled must leave the 取消 group");
  assert.deepEqual(groups.archived?.sort(), ["archived-cancelled", "archived-success"], "archived tasks get their own group");
  const grouped = (ended.groups || []).reduce((sum, group) => sum + group.tasks.length, 0);
  assert.equal(grouped, ended.tasks.length, "column header count must match the sum of its groups");
});

test("the archived group is collapsed by default", () => {
  const tasks = [
    terminalTask({ id: "done", name: "DONE-TASK" }),
    terminalTask({ id: "archived", name: "ARCHIVED-TASK", archived_at: "2026-11-18T00:00:00Z" }),
  ];
  const { kanbanColumns } = useAppKanbanColumns({ kanbanTasks: tasks, t: (key) => key });
  const boardProps = (collapsedTaskCompletionGroups) => ({
    workspaceOpen: false,
    currentRootId: "root-1",
    currentRootIdRef: { current: "root-1" },
    workspaceBoard: { projects: [] },
    workspaceFilter: "all",
    onWorkspaceFilterChange: () => {},
    workspaceCollapsedKeys: new Set(),
    onToggleWorkspaceProject: () => {},
    onRefreshWorkspaceBoard: () => {},
    managedRootIds: ["root-1"],
    getRootDisplayName: () => "root",
    openWorkspaceProject: async () => {},
    openWorkspaceTaskDetail: async () => {},
    handleWorkspaceCreateTask: () => {},
    setSelectedKanbanTaskId: () => {},
    kanbanTasksLoading: false,
    kanbanColumns,
    selectedKanbanTaskId: "",
    expandedTaskInputIds: new Set(),
    setExpandedTaskInputIds: () => {},
    collapsedTaskCompletionGroups,
    setCollapsedTaskCompletionGroups: () => {},
    collapsedKanbanColumns: new Set(),
    setCollapsedKanbanColumns: () => {},
    taskFirstInputById: {},
    taskSessionKeysById: {},
    sessionByKey: {},
    getDisplayNodeColor: () => null,
    handleSelectKanbanTask: () => {},
    handleMoveKanbanTask: async () => {},
    openTaskCreateDialog: () => {},
    handleTaskSessionDrawerOpen: () => {},
    setTaskSessionErrorDialog: () => {},
    loadKanbanTasks: async () => {},
    onOpenWorkflowEditor: () => {},
  });
  const renderBoard = (collapsed) => renderToString(
    React.createElement(I18nProvider, null, React.createElement(TaskBoardView, boardProps(collapsed))),
  );

  const defaultHtml = renderBoard(new Set(["archived"]));
  assert.doesNotMatch(defaultHtml, /ARCHIVED-TASK/, "archived cards must be hidden by default");
  assert.match(defaultHtml, /DONE-TASK/, "completed cards stay visible");

  const expandedHtml = renderBoard(new Set());
  assert.match(expandedHtml, /ARCHIVED-TASK/, "clicking the group header must reveal archived cards");
});

test("a terminal card offers archive next to delete", () => {
  const html = renderCard(terminalTask());
  assert.match(html, /归档任务|Archive task/, "terminal cards must offer archive");
  assert.doesNotMatch(html, /取消归档|Unarchive/, "a live card must not offer unarchive");
  assert.match(html, /回到待处理|Return to Awaiting action/, "unarchived cards keep the reopen entry");
});

test("an archived card offers reopen + unarchive + delete", () => {
  // 2026-11-22 用户裁定「已归档以卡片为准，卡片补『返回』」：归档卡要先能拿回
  // 活动列，所以「返回」与「取消归档」并列，删除照旧。
  const html = renderCard(terminalTask({ archived_at: "2026-11-18T00:00:00Z" }));
  assert.match(html, /取消归档|Unarchive/, "archived cards must offer unarchive");
  assert.doesNotMatch(html, /归档任务|Archive task/, "archived cards must not offer archive again");
  assert.match(html, /回到待处理|Return to Awaiting action/, "archived cards keep the reopen entry");
  assert.match(html, /删除任务卡片|Delete task card/, "delete must stay available");
  // 归档卡只剩三个操作键：返回 + 取消归档 + 删除。
  const buttons = html.match(/<button/g) || [];
  assert.equal(buttons.length, 3, `archived card must render exactly 3 action buttons, got ${buttons.length}`);
});

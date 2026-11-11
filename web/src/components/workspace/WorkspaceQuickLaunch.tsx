import React from "react";
import { useI18n } from "../../i18n";
import { PlusSmallIcon } from "../task/taskIcons";
import type { WorkspaceProjectGroup } from "../../app/useWorkspaceBoard";

/**
 * 快速发起：工作台工具栏上的一个按钮，点开直接就是**新建任务面板本身**。
 *
 * 曾经中间还夹着一层「选项目 + 选模板」的小弹窗，点完才打开新建任务面板 ——
 * 两层弹窗、两次点击，而且第二层里真正能改的东西全被挡在后面。现在打开的就是
 * 新建任务面板，只多一个「项目」下拉（见 TaskInlineEditState.allowProjectSwitch）：
 * worktree、agent、附件、立即执行全都直接可改，一层到位。
 *
 * 模板选择已随模板入口一起下线（Phase 1）：面板固定使用内置默认流水。
 *
 * 按钮本身不选项目 —— 落在第一个项目上，进去之后在下拉里改。这样「点一下 →
 * 就在某个项目里起草」和看板上的新建按钮是同一套心智。
 */
export function WorkspaceQuickLaunch({ projects, onPick }: {
  projects: WorkspaceProjectGroup[];
  onPick: (rootId: string, nodeId: string) => void;
}) {
  const { t } = useI18n();
  // 工作台只显示有任务的项目，所以「选哪个项目」和「看哪个项目」永远同一批。
  const group = projects[0];
  if (!group) return null;
  return (
    <button
      type="button"
      data-onboarding="task-create"
      title={t("task.workspaceQuickLaunch")}
      aria-label={t("task.workspaceQuickLaunch")}
      onClick={() => onPick(group.rootId, group.nodeId)}
      style={{
        height: "22px",
        padding: "0 9px",
        borderRadius: "6px",
        border: "1px solid var(--border-color)",
        background: "transparent",
        color: "var(--text-color)",
        fontSize: "11px",
        fontWeight: 700,
        display: "inline-flex",
        alignItems: "center",
        gap: "4px",
        cursor: "pointer",
        whiteSpace: "nowrap",
      }}
    >
      <PlusSmallIcon />
      {t("task.workspaceQuickLaunch")}
    </button>
  );
}

import { useI18n } from "../../i18n/index";
import type { KanbanTask } from "../../shared/taskTypes";
import type { TaskCardAction } from "./taskBoardTypes";
import { TaskFinishWorktreeIcon, taskCardIconButtonStyle } from "./taskIcons";

/**
 * 收尾键。看板卡与工作台卡**两处都要它**（未终态 + 终态），抽成一个组件免得
 * 抄两遍 —— 抄两遍的话，改一处忘另一处就是「两边卡片不一致」的下一个来源。
 *
 * 放在执行键右边：两者是任务生命周期的两端（继续跑 / 跑完收掉），挨着才看得出
 * 这是一对。**收尾中也照样给**（2026-10-05）：那正是「agent 那半已经做完、只差
 * 机械清场」的时刻，后端会直接清场；换成转圈等于把唯一的出路藏起来。
 *
 * **终态也给**（2026-10-07）：任务跑完但 worktree 还留着没收，那正是收尾唯一
 * 有意义的时刻；服务端 reviveTerminalTask 会把它拉回 waiting_user 再让收尾段跑。
 */

export type TaskCardFinishButtonProps = {
  task: KanbanTask;
  onMove: (task: KanbanTask, action: TaskCardAction) => void;
};

export function TaskCardFinishButton({ task, onMove }: TaskCardFinishButtonProps) {
  const { t } = useI18n();
  return (
    <button
      type="button"
      title={t("task.finishWorktree")}
      aria-label={t("task.finishWorktree")}
      onClick={(event) => {
        event.stopPropagation();
        onMove(task, "finish-worktree");
      }}
      style={taskCardIconButtonStyle("success")}
    >
      <TaskFinishWorktreeIcon />
    </button>
  );
}

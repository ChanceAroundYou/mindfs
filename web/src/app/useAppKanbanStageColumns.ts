import { useI18n } from "../i18n";
import type { KanbanTask } from "../services/tasks";

/**
 * 看板五列（待开始/进行中/等待你/已结束 + 已结束里的成功/取消分组）。
 */
export type UseAppKanbanStageColumnsParams = {
  kanbanTasks: KanbanTask[];
  t: ReturnType<typeof useI18n>["t"];
};

/**
 * 列 = 全局状态，不是每个任务自己的流水阶段。前四列的数字直接来自按 status 过滤，
 * 「已结束」再按 success / fail 分成两个 tone 不同的子组。
 *
 * **别在这里滤掉空分组**：TaskBoardView 用「groups 非空」判断要不要画分组行；
 * 滤掉「已完成」后，只要「已取消」还有任务，列头就会显示两状态总和，
 * 于是出现「列头有数字、里面只有一个分组」的错位。
 */
export function useAppKanbanStageColumns(params: UseAppKanbanStageColumnsParams) {
  const { kanbanTasks, t } = params;

  const kanbanStageColumns: Array<{
    index: number;
    name: string;
    role: "user" | "agent";
    tasks: KanbanTask[];
    groups?: Array<{ key: string; name: string; tone: "success" | "danger" | "muted"; tasks: KanbanTask[] }>;
  }> = [
    {
      index: 0,
      name: t("task.column.pending"),
      role: "user" as const,
      tasks: kanbanTasks.filter((task) => task.status === "pending"),
    },
    {
      index: 1,
      name: t("task.column.running"),
      role: "agent" as const,
      tasks: kanbanTasks.filter((task) => task.status === "running" || task.status === "paused"),
    },
    {
      index: 2,
      name: t("task.column.waitingUser"),
      role: "user" as const,
      tasks: kanbanTasks.filter((task) => task.status === "waiting_user"),
    },
    {
      index: 3,
      // 已结束 = 完成 + 取消；失败合并进取消。
      name: t("task.column.ended"),
      role: "user" as const,
      tasks: kanbanTasks.filter((task) => task.status === "success" || task.status === "fail" || task.status === "cancelled"),
      groups: [{
        key: "success",
        name: t("task.group.completed"),
        tone: "success" as const,
        tasks: kanbanTasks.filter((task) => task.status === "success"),
      }, {
        key: "cancelled",
        name: t("task.group.cancelled"),
        tone: "muted" as const,
        tasks: kanbanTasks.filter((task) => task.status === "fail" || task.status === "cancelled"),
      }],
      // 别在这里滤掉空分组：TaskBoardView 用「groups 非空」决定走不走分组渲染，
      // 滤掉「已完成」后，只要「已取消」有任务就整组看不见——列头却仍按 tasks.length
      // 显示两状态总和，于是出现「列头有数字、里面找不到对应组」。空组由渲染层显示成 0。
    },
  ];

  return {
    kanbanStageColumns,
  };
}

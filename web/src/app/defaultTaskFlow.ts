/**
 * 内置默认任务流：一个 user 段 + 一个 agent 段。
 *
 * 任务模板已下线（Phase 3）：新建任务不再选模板，前端始终随包带上这份流水。
 * 它按 wire 格式（拍平的 StageTemplate[]）给出，创建时直接落成任务的
 * `task_stages_json` 快照，与历史模板创建时拷贝的快照语义一致。
 */
import type { StageTemplate } from "../shared/taskTypes";
import { DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL } from "./appTask";

export function defaultTaskFlowStages(): StageTemplate[] {
  return [
    {
      name: "",
      role: "user",
      // user 段的推进由引擎固定，这里存 true 只为让 JSON 与行为一致。
      auto_advance: true,
      start_immediately: false,
      prompt_template: "",
    },
    {
      name: "",
      role: "agent",
      auto_advance: false,
      agent: DEFAULT_TASK_AGENT,
      model: DEFAULT_TASK_MODEL,
      prompt_template: "{previous_input}",
    },
  ];
}

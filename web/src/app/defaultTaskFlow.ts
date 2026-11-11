/**
 * 内置默认任务流：一个 user 段 + 一个 agent 段。
 *
 * 任务模板入口下线后（Phase 1），新建任务面板不再要求先选模板，后端也不应
 * 再去「任务所在节点」回查模板库（模板库只在主节点，worker 上必然查不到）。
 * 所以前端始终随包带上这份流水；它与模板创建时拷贝的快照语义一致，只是来源
 * 是本文件而不是 template store。
 *
 * 这个 id 只在本机流转，不会作为 task_template_id 落库 —— 保存时会被归一成 ""。
 */
import type { TaskTemplate } from "../shared/taskTypes";
import { DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL } from "./appTask";

export const DEFAULT_TASK_FLOW_ID = "__builtin_default_flow__";

export function defaultTaskFlowTemplate(): TaskTemplate {
  return {
    id: DEFAULT_TASK_FLOW_ID,
    name: "",
    description: "",
    stages: [
      {
        position: 0,
        snapshot: {
          name: "",
          role: "user",
          // user 段的推进由引擎固定，这里存 true 只为让 JSON 与行为一致。
          auto_advance: true,
          start_immediately: false,
          prompt_template: "",
        },
      },
      {
        position: 1,
        snapshot: {
          name: "",
          role: "agent",
          auto_advance: false,
          agent: DEFAULT_TASK_AGENT,
          model: DEFAULT_TASK_MODEL,
          prompt_template: "{previous_input}",
        },
      },
    ],
  };
}
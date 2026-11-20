// Router 决策的列表投影（后端 routerDecisionSummary 的镜像）。
//
// 只含列表端点实际下发的字段：动作全文、brief、转录留在详情端点，列表这里
// 只要「谁在什么时候做了什么、结果如何」。

export type RouterDecisionSummary = {
  id: string;
  task_id: string;
  trigger_kind: string;
  trigger_ref?: string;
  user_activity_id?: string;
  placeholder_activity_id?: string;
  status: string;
  agreement?: string;
  action_kinds?: string[];
  legacy_kind?: string;
  error?: string;
  latency_ms?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  created_at: string;
};

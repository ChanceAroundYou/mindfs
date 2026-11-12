// Workflow 元数据与推荐入口（对话式任务创建时的自动选择）。
//
// `/api/workflows` 在控制面前缀表里（nodeinfo.ControlPlanePrefixes），worker 上
// 一律 403 —— 所以这里必须走 controlPath 打页面服务器，**不能**用 appURL：
// 那会跟着当前选中节点走，在 PC/fn 上打开任务面板时直接拿不到列表。
import { controlPath } from "./net/controlPlane";
import { protectedJSON } from "./net/api";
import type { WorkflowMeta, WorkflowRecommendation } from "../shared/workflowModel";

export type { WorkflowMeta, WorkflowRecommendation } from "../shared/workflowModel";

const AVAILABLE_PATH = "/api/workflows/available";
const RECOMMEND_PATH = "/api/workflows/recommend";

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

/**
 * Go 的 nil slice 编码成 `null` —— 组件会直接读 `wf.tags.length`，所以缺字段
 * 必须在这里归一成 `[]`，不能把 null 透给视图层。
 */
function normalizeWorkflowMeta(value: unknown): WorkflowMeta {
  const record = value && typeof value === "object" ? (value as Record<string, unknown>) : {};
  const meta: WorkflowMeta = {
    id: typeof record.id === "string" ? record.id : "",
    name: typeof record.name === "string" ? record.name : "",
    description: typeof record.description === "string" ? record.description : "",
    use_cases: stringList(record.use_cases),
    tags: stringList(record.tags),
  };
  if (typeof record.auto_select_enabled === "boolean") meta.auto_select_enabled = record.auto_select_enabled;
  if (typeof record.recommended === "boolean") meta.recommended = record.recommended;
  if (typeof record.match_score === "number" && Number.isFinite(record.match_score)) {
    meta.match_score = record.match_score;
  }
  return meta;
}

/**
 * 列出可自动选择的 workflow。传 `userInput` 时后端会附带推荐标记
 * （`recommended` / `match_score`）；推荐失败不阻断列表，只是没有标记。
 */
export async function listAvailableWorkflows(userInput?: string): Promise<WorkflowMeta[]> {
  const params = new URLSearchParams();
  const trimmed = userInput?.trim();
  if (trimmed) params.set("user_input", trimmed);
  const payload = await protectedJSON<{ workflows?: unknown }>(controlPath(AVAILABLE_PATH, params));
  if (!Array.isArray(payload?.workflows)) return [];
  return payload.workflows.map(normalizeWorkflowMeta).filter((item) => item.id !== "");
}

/** 让后端为一段用户输入推荐一个 workflow。空输入没有推荐语义，直接拒绝。 */
export async function recommendWorkflow(userInput: string): Promise<WorkflowRecommendation> {
  const trimmed = userInput.trim();
  if (!trimmed) throw new Error("user_input required");
  const payload = await protectedJSON<{ recommended?: Partial<WorkflowRecommendation> }>(
    controlPath(RECOMMEND_PATH),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_input: trimmed }),
    },
  );
  const rec = payload?.recommended;
  if (!rec || typeof rec.workflow_id !== "string" || !rec.workflow_id.trim()) {
    throw new Error("workflow recommendation missing workflow_id");
  }
  return {
    workflow_id: rec.workflow_id,
    reason: typeof rec.reason === "string" ? rec.reason : "",
    match_score:
      typeof rec.match_score === "number" && Number.isFinite(rec.match_score) ? rec.match_score : 0,
  };
}
// 完成证据清单的线上形状（Plan 2026-11-20 Task 8）。字段与后端
// GET /api/tasks/{id}/completion 一一对应：items 是当时落库的那份报告，
// verifier_verdict 是独立复核的结论（空串 = 没有复核）。
export type CompletionItem = {
  index: number;
  id: string;
  label: string;
  kind: string;
  pass: boolean;
  detail: string;
};

export type TaskCompletionView = {
  present: boolean;
  status: string;
  items: CompletionItem[];
  passed: boolean;
  repairs_used: number;
  repairs_max: number;
  verifier_verdict: string;
  updated_at: string;
};

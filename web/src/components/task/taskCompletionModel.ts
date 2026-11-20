import type { MessageKey } from "../../i18n/types";
import type { CompletionItem, TaskCompletionView } from "../../shared/taskCompletion";

// 完成证据清单的纯函数（Plan 2026-11-20 Task 8）：编号、文案键、预算。
// 文案键只为「认识的取值」返回 key；后端将来新增没登记的状态原样显示，不吞信息。

export type CompletionRow = {
  number: number;
  id: string;
  label: string;
  kind: string;
  pass: boolean;
  detail: string;
};

// completionRows 按报告顺序编号。label 为空时回落到 id：证据项永远有身份，
// 空标签不该让界面出现一条谁也认不出的空白行。
export function completionRows(completion: TaskCompletionView | null): CompletionRow[] {
  if (!completion || !Array.isArray(completion.items)) return [];
  return completion.items.map((item: CompletionItem, index: number) => ({
    number: index + 1,
    id: item.id,
    label: (item.label || "").trim() || item.id,
    kind: item.kind,
    pass: item.pass === true,
    detail: (item.detail || "").trim(),
  }));
}

export function completionVerdictKey(verdict: string): MessageKey | null {
  switch ((verdict || "").trim()) {
    case "pass":
      return "task.completion.verdict.pass";
    case "fail":
      return "task.completion.verdict.fail";
    case "uncertain":
      return "task.completion.verdict.uncertain";
    default:
      return null;
  }
}

export function completionStatusKey(status: string): MessageKey | null {
  switch ((status || "").trim()) {
    case "verifying":
      return "task.completion.status.verifying";
    case "blocked":
      return "task.completion.status.blocked";
    case "completed":
      return "task.completion.status.completed";
    default:
      return null;
  }
}

import React from "react";
import { useI18n } from "../../i18n/index";
import type { TaskCompletionView } from "../../shared/taskCompletion";
import { completionRows, completionStatusKey, completionVerdictKey } from "./taskCompletionModel";

// 完成证据清单（Plan 2026-11-20 Task 8）：只读证据行，回答「这一轮还差什么」。
//
// 两种来源必须分开标：机检项（契约）是硬证据；复核结论是模型判定，标签写死
// 「模型判定」，不能让它看起来和机检一样可信。
//
// 只在胶囊展开后渲染（见 TaskCompletionEntry）。所以这里不再重复「完成证据」
// 标题 —— 胶囊已经写着；正文只留状态、补做预算与失败项。
export function TaskCompletionChecklist({ completion }: { completion: TaskCompletionView | null }) {
  const { t } = useI18n();
  if (!completion || !completion.present) return null;

  const rows = completionRows(completion);
  const statusKey = completionStatusKey(completion.status);
  const verdictKey = completionVerdictKey(completion.verifier_verdict);
  const failed = rows.filter((row) => !row.pass);

  return (
    <div
      data-testid="task-completion-checklist"
      style={{ display: "flex", flexDirection: "column", gap: "6px", minWidth: 0, fontSize: "12px" }}
    >
      <div style={{ display: "flex", alignItems: "baseline", gap: "8px", flexWrap: "wrap" }}>
        <span style={{ fontWeight: 600 }}>{statusKey ? t(statusKey) : completion.status}</span>
        <span style={{ color: "var(--text-secondary)", fontVariantNumeric: "tabular-nums" }}>
          {t("task.completion.repairs", { used: completion.repairs_used, max: completion.repairs_max })}
        </span>
      </div>
      {failed.length === 0 ? (
        <div style={{ color: "var(--text-secondary)" }}>{t("task.completion.allPassed")}</div>
      ) : (
        <ol
          data-testid="task-completion-items"
          style={{ margin: 0, paddingLeft: "18px", display: "flex", flexDirection: "column", gap: "6px" }}
        >
          {failed.map((row) => (
            <li key={row.id} value={row.number} data-testid={`task-completion-item-${row.id}`} style={{ minWidth: 0 }}>
              <div style={{ lineHeight: 1.45 }}>{row.label}</div>
              {row.detail ? <div style={detailStyle}>{row.detail}</div> : null}
            </li>
          ))}
        </ol>
      )}
      {verdictKey ? (
        <div
          data-testid="task-completion-verdict"
          style={{ display: "flex", alignItems: "baseline", gap: "6px" }}
        >
          <span
            style={{
              fontSize: "10px",
              padding: "1px 4px",
              borderRadius: "4px",
              border: "1px solid var(--border-color)",
              color: "var(--text-secondary)",
            }}
          >
            {t("task.completion.modelVerdict")}
          </span>
          <span>{t(verdictKey)}</span>
        </div>
      ) : null}
    </div>
  );
}

// 明细独占一行：以前是「标签：明细」inline 排，一条验收项的几百字长句会把整段
// 挤成三种字号混排的砖块。长 URL / 长 token 在卡片内折行，不撑出横向滚动。
const detailStyle: React.CSSProperties = {
  color: "var(--text-secondary)",
  lineHeight: 1.5,
  overflowWrap: "anywhere",
};

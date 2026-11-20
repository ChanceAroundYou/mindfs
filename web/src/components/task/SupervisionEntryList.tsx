import React from "react";
import { useI18n } from "../../i18n/index";
import {
  supervisionActionKey,
  supervisionRowClock,
  supervisionStatusKey,
  supervisionTriggerKey,
  type SupervisionRow,
} from "./supervisionEntryModel";

// 监管记录的行（presenter）：触发 → 动作 → 结果，一行一条决策。
// 文案全走 i18n；后端将来新增没登记的字面量原样显示，不吞信息。

function statusColor(status: string): string {
  switch (status) {
    case "applied":
      return "var(--success-color, #10b981)";
    case "rejected":
    case "failed":
      return "var(--danger-color, #ef4444)";
    default:
      return "var(--text-secondary)";
  }
}

export function SupervisionEntryList({ rows }: { rows: SupervisionRow[] }) {
  const { t } = useI18n();
  return (
    <div data-testid="supervision-list" style={{ display: "flex", flexDirection: "column", gap: "4px" }}>
      {rows.map((row) => {
        const triggerKey = supervisionTriggerKey(row.trigger);
        const statusKey = supervisionStatusKey(row.status);
        return (
          <div key={row.id} data-testid={`supervision-row-${row.id}`} style={{ fontSize: "12px", lineHeight: 1.5 }}>
            <div style={{ display: "flex", alignItems: "baseline", gap: "6px", flexWrap: "wrap" }}>
              <span
                title={row.createdAt}
                style={{ fontSize: "11px", color: "var(--text-secondary)", fontVariantNumeric: "tabular-nums" }}
              >
                {supervisionRowClock(row.createdAt)}
              </span>
              <span style={{ fontWeight: 600 }}>{triggerKey ? t(triggerKey) : row.trigger}</span>
              <span style={{ color: "var(--text-secondary)" }}>→</span>
              <span>
                {row.actions
                  .map((kind) => {
                    const key = supervisionActionKey(kind);
                    return key ? t(key) : kind;
                  })
                  .join(" / ")}
              </span>
              <span style={{ color: statusColor(row.status) }}>{statusKey ? t(statusKey) : row.status}</span>
            </div>
            {row.error ? (
              <div style={{ fontSize: "11px", color: "var(--danger-color, #ef4444)" }}>{row.error}</div>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

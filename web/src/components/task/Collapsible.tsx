import type React from "react";

export type CollapsibleProps = {
  title: string;
  subtitle?: string;
  open: boolean;
  onToggle: () => void;
  children: React.ReactNode;
  /** 供测试定位；默认 "collapsible"。 */
  testId?: string;
};

/**
 * 受控折叠区块：折叠与否由调用方持有，组件本身不藏状态。
 *
 * 历史任务的阶段列表默认收起 —— 那些任务是旧模型留下的，展开只是审计用途。
 */
export function Collapsible({ title, subtitle, open, onToggle, children, testId }: CollapsibleProps) {
  return (
    <div data-testid={testId ?? "collapsible"} data-open={open || undefined}>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        style={{
          width: "100%",
          padding: "10px",
          border: "1px solid var(--border-color)",
          borderRadius: "8px",
          background: "var(--panel-bg)",
          textAlign: "left",
          cursor: "pointer",
          display: "flex",
          alignItems: "center",
          gap: "8px",
          color: "var(--text-color)",
        }}
      >
        <span aria-hidden="true" style={{ fontSize: "16px" }}>{open ? "▼" : "▶"}</span>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontWeight: 700, fontSize: "12px" }}>{title}</div>
          {subtitle ? (
            <div style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{subtitle}</div>
          ) : null}
        </div>
      </button>
      {open ? <div style={{ marginTop: "8px" }}>{children}</div> : null}
    </div>
  );
}
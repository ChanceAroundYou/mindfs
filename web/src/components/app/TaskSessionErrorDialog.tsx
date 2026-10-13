// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { CheckIconSmall, PlusSmallIcon, taskCardIconButtonStyle } from "../task/taskIcons";
import { useI18n } from "../../i18n";
import type { Dispatch, SetStateAction } from "react";

export interface TaskSessionErrorDialogProps {
  setTaskSessionErrorDialog: Dispatch<SetStateAction<{ title: string; message: string; details: string[]; } | null>>;
  t: ReturnType<typeof useI18n>["t"];
  taskCardIconButtonStyle: any;
  taskSessionErrorDialog: { title: string; message: string; details: string[]; } | null;
}

export function TaskSessionErrorDialog(props: TaskSessionErrorDialogProps) {
  const setTaskSessionErrorDialog = props.setTaskSessionErrorDialog;
  const t = props.t;
  const taskCardIconButtonStyle = props.taskCardIconButtonStyle;
  const taskSessionErrorDialog = props.taskSessionErrorDialog;

  return (
    <>
    {taskSessionErrorDialog ? (
            <div
              style={{
                position: "fixed",
                inset: 0,
                zIndex: 96,
                background: "rgba(15, 23, 42, 0.28)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                padding: "24px",
              }}
              onMouseDown={(event) => {
                if (event.target === event.currentTarget) {
                  setTaskSessionErrorDialog(null);
                }
              }}
            >
              <section
                style={{
                  width: "min(460px, 100%)",
                  borderRadius: "10px",
                  border: "1px solid rgba(217, 119, 6, 0.22)",
                  background: "var(--menu-bg)",
                  boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)",
                  overflow: "hidden",
                }}
              >
                <div style={{ padding: "12px 14px", borderBottom: "1px solid var(--border-color)", display: "flex", alignItems: "center", justifyContent: "space-between", gap: "10px" }}>
                  <div style={{ minWidth: 0, fontSize: "13px", fontWeight: 800, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {taskSessionErrorDialog.title}
                  </div>
                  <button type="button" aria-label={t("task.closeErrorInfo")} onClick={() => setTaskSessionErrorDialog(null)} style={taskCardIconButtonStyle()}>
                    ×
                  </button>
                </div>
                <div style={{ padding: "14px", display: "flex", flexDirection: "column", gap: "10px" }}>
                  <div
                    style={{
                      padding: "10px 12px",
                      borderRadius: "8px",
                      background: "rgba(217, 119, 6, 0.08)",
                      border: "1px solid rgba(217, 119, 6, 0.18)",
                      color: "var(--text-color)",
                      fontSize: "12px",
                      lineHeight: 1.5,
                      whiteSpace: "pre-wrap",
                      overflowWrap: "anywhere",
                    }}
                  >
                    {taskSessionErrorDialog.message}
                  </div>
                  {taskSessionErrorDialog.details.map((detail) => (
                    <div
                      key={detail}
                      style={{
                        padding: "10px 12px",
                        borderRadius: "8px",
                        background: "rgba(100, 116, 139, 0.08)",
                        border: "1px solid var(--border-color)",
                        color: "var(--text-secondary)",
                        fontSize: "12px",
                        lineHeight: 1.5,
                        whiteSpace: "pre-wrap",
                        overflowWrap: "anywhere",
                      }}
                    >
                      {detail}
                    </div>
                  ))}
                </div>
              </section>
            </div>
          ) : null}
    </>
  );
}

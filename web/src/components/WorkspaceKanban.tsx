import React, { useMemo, useState } from "react";
import { useI18n } from "../i18n";
import type { TaskOverviewItem } from "../services/tasks";

export type WorkspaceKanbanProps = {
  items: TaskOverviewItem[];
  loading: boolean;
  projects: Array<{ id: string; name: string }>;
  onOpenProject: (rootId: string) => void;
  onComplete: (item: TaskOverviewItem) => void;
  onRunNow: (item: TaskOverviewItem) => void;
  onOpenSession: (item: TaskOverviewItem, sessionKey: string) => void;
  onOpenDetail: (item: TaskOverviewItem) => void;
  onCreateTask: (rootId: string, input: string) => void;
};

// 跨项目工作台（总面板）：分区列表 —— 等待你（大卡）→ 运行中（紧凑行）→ 快速发起 → 最近归档。
// 卡上只做最简单操作；改名/段编辑等复杂操作进项目详情面板。
export function WorkspaceKanban({ items, loading, projects, onOpenProject, onComplete, onRunNow, onOpenSession, onOpenDetail, onCreateTask }: WorkspaceKanbanProps) {
  const { t } = useI18n();
  const [showAll, setShowAll] = useState(false);
  const [quickProject, setQuickProject] = useState(projects[0]?.id || "");
  const [quickInput, setQuickInput] = useState("");

  const taskName = (item: TaskOverviewItem) => item.task.name || item.root_name || item.task.task_template_name || t("task.unnamedTemplate");

  const waiting = useMemo(
    () => items.filter((item) => item.task.status === "waiting_user" || item.task.status === "pending"),
    [items],
  );
  const running = useMemo(
    () => items.filter((item) => item.task.status === "running" || item.task.status === "queued" || item.task.status === "paused"),
    [items],
  );
  const archive = useMemo(
    () => items.filter((item) => item.task.status === "success" || item.task.status === "fail" || item.task.status === "cancelled"),
    [items],
  );
  const availableProjects = useMemo(() => {
    const known = new Set(projects.map((project) => project.id));
    for (const item of items) known.add(item.root_id);
    return Array.from(known).map((id) => ({
      id,
      name: projects.find((project) => project.id === id)?.name || id,
    }));
  }, [projects, items]);

  const submitQuick = () => {
    const input = quickInput.trim();
    const rootId = quickProject || availableProjects[0]?.id || "";
    if (!input || !rootId) return;
    onCreateTask(rootId, input);
    setQuickInput("");
  };

  const renderCardButtons = (item: TaskOverviewItem, big: boolean) => {
    const status = item.task.status;
    const btn = (kind: "success" | "accent", label: string, action: () => void, hidden = false) =>
      hidden ? null : (
        <button
          type="button"
          onClick={(event) => { event.stopPropagation(); action(); }}
          style={{
            height: big ? "26px" : "22px",
            borderRadius: "6px",
            border: "1px solid var(--border-color)",
            background: kind === "success" ? "rgba(22, 163, 74, 0.12)" : "rgba(37, 99, 235, 0.10)",
            color: kind === "success" ? "#16a34a" : "var(--accent-color)",
            padding: big ? "0 10px" : "0 7px",
            fontSize: big ? "12px" : "11px",
            fontWeight: 700,
            cursor: "pointer",
            whiteSpace: "nowrap",
            flexShrink: 0,
          }}
        >
          {label}
        </button>
      );
    return (
      <>
        {/* 完成只改任务状态，不依赖会话/worktree，所以非终态都给。 */}
        {status !== "success" && status !== "fail" && status !== "cancelled"
          ? btn("success", t("task.completeShort"), () => onComplete(item))
          : null}
        {status === "waiting_user" || status === "pending"
          ? btn("accent", t("task.runNow"), () => onRunNow(item))
          : null}
      </>
    );
  };

  const rowLine = (item: TaskOverviewItem) => (
    <>
      <span style={{ color: "#0ea5e9", fontWeight: 800, flexShrink: 0 }}>{item.task.task_number ? `#${item.task.task_number}` : ""}</span>
      <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: 800, color: "var(--text-color)" }}>
        {taskName(item)}
      </span>
      <button
        type="button"
        onClick={(event) => { event.stopPropagation(); onOpenProject(item.root_id); }}
        title={item.root_name}
        style={{
          flexShrink: 0,
          height: "18px",
          borderRadius: "6px",
          border: "none",
          background: "rgba(148, 163, 184, 0.14)",
          color: "var(--text-secondary)",
          padding: "0 7px",
          fontSize: "10px",
          fontWeight: 700,
          cursor: "pointer",
          whiteSpace: "nowrap",
        }}
      >
        {item.root_name || item.root_id}
      </button>
      <div style={{ marginLeft: "auto", display: "flex", gap: "4px", flexShrink: 0 }}>
        {renderCardButtons(item, false)}
      </div>
    </>
  );

  return (
    <div style={{ overflowY: "auto", padding: "12px", display: "flex", flexDirection: "column", gap: "14px", minHeight: 0, maxHeight: "calc(100dvh - 148px)" }}>
      {/* ⏳ 等待你 */}
      <section style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
        <header style={{ display: "flex", alignItems: "center", gap: "6px" }}>
          <span style={{ fontSize: "13px", fontWeight: 800, color: "#b45309" }}>{t("task.column.waitingUser")}</span>
          <span style={{ fontSize: "11px", fontWeight: 800, color: "var(--text-secondary)" }}>{waiting.length}</span>
        </header>
        {loading && items.length === 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.loading")}</div>
        ) : waiting.length === 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.workspaceNothingWaiting")}</div>
        ) : (
          waiting.map((item) => (
            <article
              key={`w-${item.task.id}`}
              onClick={() => onOpenDetail(item)}
              style={{
                border: "1px solid rgba(180, 83, 9, 0.35)",
                borderRadius: "10px",
                background: "var(--menu-bg)",
                padding: "12px",
                boxShadow: "0 1px 3px rgba(15, 23, 42, 0.08)",
                cursor: "pointer",
                display: "flex",
                flexDirection: "column",
                gap: "8px",
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
                <span style={{ color: "#0ea5e9", fontWeight: 800, fontSize: "13px" }}>{item.task.task_number ? `#${item.task.task_number}` : ""}</span>
                <span style={{ fontSize: "14px", fontWeight: 800, color: "var(--text-color)", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {taskName(item)}
                </span>
                <button
                  type="button"
                  onClick={(event) => { event.stopPropagation(); onOpenProject(item.root_id); }}
                  style={{
                    height: "20px",
                    borderRadius: "6px",
                    border: "none",
                    background: "rgba(148, 163, 184, 0.14)",
                    color: "var(--text-secondary)",
                    padding: "0 8px",
                    fontSize: "11px",
                    fontWeight: 700,
                    cursor: "pointer",
                    whiteSpace: "nowrap",
                  }}
                >
                  {item.root_name || item.root_id}
                </button>
                {item.task.main_session_key ? (
                  <button
                    type="button"
                    onClick={(event) => { event.stopPropagation(); onOpenSession(item, item.task.main_session_key as string); }}
                    title={t("task.openSession", { index: 1 })}
                    style={{
                      height: "20px",
                      borderRadius: "6px",
                      border: "1px solid var(--border-color)",
                      background: "var(--button-bg)",
                      color: "var(--text-color)",
                      padding: "0 8px",
                      fontSize: "11px",
                      fontWeight: 700,
                      cursor: "pointer",
                    }}
                  >
                    {t("task.sessionTitle")}
                  </button>
                ) : null}
                <div style={{ marginLeft: "auto", display: "flex", gap: "6px" }}>
                  {renderCardButtons(item, true)}
                </div>
              </div>
            </article>
          ))
        )}
      </section>

      {/* ▶ 运行中 */}
      <section style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
        <header style={{ display: "flex", alignItems: "center", gap: "6px" }}>
          <span style={{ fontSize: "13px", fontWeight: 800, color: "#2563eb" }}>{t("task.column.running")}</span>
          <span style={{ fontSize: "11px", fontWeight: 800, color: "var(--text-secondary)" }}>{running.length}</span>
        </header>
        {running.length === 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.workspaceNothingRunning")}</div>
        ) : (
          running.map((item) => (
            <article
              key={`r-${item.task.id}`}
              onClick={() => onOpenDetail(item)}
              style={{
                border: "1px solid var(--border-color)",
                borderRadius: "8px",
                background: "var(--menu-bg)",
                padding: "6px 10px",
                cursor: "pointer",
                display: "flex",
                alignItems: "center",
                gap: "8px",
              }}
            >
              {rowLine(item)}
            </article>
          ))
        )}
      </section>

      {/* + 快速发起 */}
      <section style={{ border: "1px dashed var(--border-color)", borderRadius: "10px", padding: "10px", display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap", background: "var(--menu-bg)" }}>
        <span style={{ fontSize: "12px", fontWeight: 800, color: "var(--text-secondary)" }}>{t("task.workspaceQuickLaunch")}</span>
        <select
          value={quickProject || availableProjects[0]?.id || ""}
          onChange={(event) => setQuickProject(event.target.value)}
          style={{ height: "28px", borderRadius: "6px", border: "1px solid var(--border-color)", background: "var(--input-bg)", color: "var(--text-color)", fontSize: "12px", maxWidth: "180px" }}
        >
          {availableProjects.map((project) => (
            <option key={project.id} value={project.id}>{project.name}</option>
          ))}
        </select>
        <input
          value={quickInput}
          onChange={(event) => setQuickInput(event.target.value)}
          placeholder={t("task.quickLaunchPlaceholder")}
          onKeyDown={(event) => { if (event.key === "Enter") submitQuick(); }}
          style={{ flex: "1 1 auto", minWidth: "160px", height: "28px", borderRadius: "6px", border: "1px solid var(--border-color)", background: "var(--input-bg)", color: "var(--text-color)", padding: "0 8px", fontSize: "12px", outline: "none" }}
        />
        <button
          type="button"
          disabled={!quickInput.trim()}
          onClick={submitQuick}
          style={{
            height: "28px",
            borderRadius: "6px",
            border: "1px solid var(--accent-color)",
            background: quickInput.trim() ? "var(--accent-color)" : "var(--button-bg)",
            color: quickInput.trim() ? "#fff" : "var(--text-secondary)",
            padding: "0 12px",
            fontSize: "12px",
            fontWeight: 700,
            cursor: quickInput.trim() ? "pointer" : "not-allowed",
          }}
        >
          {t("task.quickLaunchSend")}
        </button>
      </section>

      {/* 最近完成 / 归档 */}
      <section style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
        <header style={{ display: "flex", alignItems: "center", gap: "8px" }}>
          <span style={{ fontSize: "13px", fontWeight: 800, color: "var(--text-secondary)" }}>
            {showAll ? t("task.workspaceAll") : t("task.workspaceRecent")}
          </span>
          <button
            type="button"
            onClick={() => setShowAll((value) => !value)}
            style={{
              height: "22px",
              borderRadius: "6px",
              border: "1px solid var(--border-color)",
              background: "var(--button-bg)",
              color: "var(--text-secondary)",
              padding: "0 8px",
              fontSize: "11px",
              fontWeight: 700,
              cursor: "pointer",
            }}
          >
            {showAll ? t("task.workspaceRecent") : t("task.workspaceAll")}
          </button>
        </header>
        {(() => {
          const shown = showAll ? archive : archive.slice(0, 5);
          if (shown.length === 0) return <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.workspaceNoArchive")}</div>;
          return shown.map((item) => (
            <article
              key={`a-${item.task.id}`}
              onClick={() => onOpenDetail(item)}
              style={{
                border: "1px solid var(--border-color)",
                borderRadius: "8px",
                background: "var(--menu-bg)",
                padding: "6px 10px",
                cursor: "pointer",
                display: "flex",
                alignItems: "center",
                gap: "8px",
                opacity: 0.92,
              }}
            >
              {rowLine(item)}
            </article>
          ));
        })()}
      </section>
    </div>
  );
}

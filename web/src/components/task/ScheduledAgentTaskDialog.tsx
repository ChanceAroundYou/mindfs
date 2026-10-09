import React, { useEffect, useMemo, useRef, useState } from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { AgentSelector } from "../agent/AgentSelector";
import { renderToolIcon } from "../stream/toolCallIcons";
import { has1MSuffix, resolveEffortOnSwitch, resolveLongContextOnSwitch, strip1MSuffix, with1MSuffix } from "../action/modelUtils";
import type { AgentStatus } from "../../services/agents";
import {
  createScheduledAgentTask,
  deleteScheduledAgentTask,
  fetchScheduledAgentTasks,
  runScheduledAgentTask,
  updateScheduledAgentTask,
  type ScheduledAgentTask,
} from "../../services/scheduledTasks";
import { useI18n, type Locale } from "../../i18n/index";
import { confirmDialog } from "../../services/dialog";
import {
  CronEditor,
  DeleteIcon,
  RunNowIcon,
  StopIcon,
  fieldStyle,
  taskIconButtonStyle,
} from "./ScheduledCronEditor";
import {
  emptyForm,
  formatTime,
  taskToForm,
  menuButtonStyle,
  strategyButtonStyle,
  type FormState,
} from "./scheduledTaskUtils";

type DialogView = "list" | "create" | "edit";

type Props = {
  open: boolean;
  rootId?: string | null;
  agents: AgentStatus[];
  onClose: () => void;
};


export function ScheduledAgentTaskDialog({
  open,
  rootId,
  agents,
  onClose,
}: Props) {
  const { locale, t } = useI18n();
  const [view, setView] = useState<DialogView>("list");
  const [tasks, setTasks] = useState<ScheduledAgentTask[]>([]);
  const [selected, setSelected] = useState<ScheduledAgentTask | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm());
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [runningTaskId, setRunningTaskId] = useState<string | null>(null);
  const dialogBodyRef = useRef<HTMLDivElement | null>(null);

  const defaultAgent = useMemo(
    () => agents.find((item) => item.available)?.name || agents[0]?.name || "",
    [agents],
  );

  const loadTasks = async () => {
    if (!rootId) return;
    setLoading(true);
    setError("");
    try {
      setTasks(await fetchScheduledAgentTasks(rootId));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!open) return;
    setView("list");
    setSelected(null);
    setForm(emptyForm(defaultAgent));
    void loadTasks();
  }, [open, rootId, defaultAgent]);

  useEffect(() => {
    if (!open) return;
    const body = dialogBodyRef.current;
    if (!body) return;
    const scrollFocusedInputIntoView = (target: HTMLElement) => {
      const bodyRect = body.getBoundingClientRect();
      const targetRect = target.getBoundingClientRect();
      const bodyCenter = bodyRect.top + bodyRect.height * 0.45;
      const targetCenter = targetRect.top + targetRect.height / 2;
      body.scrollTo({
        top: Math.max(0, body.scrollTop + targetCenter - bodyCenter),
        behavior: "smooth",
      });
    };
    const handleFocusIn = (event: FocusEvent) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) return;
      if (!target.matches("input, textarea")) return;
      for (const delay of [80, 260, 520]) {
        window.setTimeout(() => scrollFocusedInputIntoView(target), delay);
      }
    };
    body.addEventListener("focusin", handleFocusIn);
    return () => body.removeEventListener("focusin", handleFocusIn);
  }, [open]);

  if (!open) return null;

  const startCreate = () => {
    setSelected(null);
    setForm(emptyForm(defaultAgent));
    setError("");
    setView("create");
  };

  const startEdit = (task: ScheduledAgentTask) => {
    setSelected(task);
    setForm(taskToForm(task));
    setError("");
    setView("edit");
  };

  const save = async () => {
    if (!rootId || saving) return;
    if (!form.name.trim()) {
      setError(t("scheduled.nameRequired"));
      return;
    }
    setSaving(true);
    setError("");
    try {
      const payload = {
        root_id: rootId,
        name: form.name.trim(),
        enabled: form.enabled,
        task_cron: form.task_cron,
        agent: form.agent,
        model: form.model,
        mode: form.mode,
        effort: form.effort,
        fast_service: form.fast_service,
        prompt: form.prompt,
        new_session_cron: form.new_session_cron,
      };
      if (view === "edit" && selected) {
        await updateScheduledAgentTask(selected.id, payload);
      } else {
        await createScheduledAgentTask(payload);
      }
      await loadTasks();
      setView("list");
      setSelected(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (task: ScheduledAgentTask) => {
    if (!rootId) return;
    if (!await confirmDialog({ message: t("scheduled.confirmDelete", { name: task.name || task.id }), danger: true })) {
      return;
    }
    setError("");
    try {
      await deleteScheduledAgentTask(rootId, task.id);
      await loadTasks();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const runNow = async (task: ScheduledAgentTask) => {
    if (!rootId || runningTaskId) return;
    setRunningTaskId(task.id);
    setError("");
    try {
      await runScheduledAgentTask(rootId, task.id);
      await loadTasks();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRunningTaskId(null);
    }
  };

  const toggleEnabled = async (task: ScheduledAgentTask) => {
    if (!rootId) return;
    setError("");
    try {
      await updateScheduledAgentTask(task.id, {
        root_id: rootId,
        name: task.name,
        enabled: !task.enabled,
        task_cron: task.task_cron,
        agent: task.agent,
        model: task.model || "",
        mode: task.mode || "",
        effort: task.effort || "",
        fast_service: task.fast_service || "",
        prompt: task.prompt,
        new_session_cron: task.new_session_cron || "",
      });
      await loadTasks();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const renderList = () => (
    <>
      {loading ? (
        <div
          style={{
            border: "1px solid var(--border-color)",
            borderRadius: 8,
            padding: 24,
            color: "var(--text-secondary)",
            textAlign: "center",
            fontSize: 13,
          }}
        >
          {t("common.loading")}
        </div>
      ) : tasks.length === 0 ? (
        <div
          style={{
            border: "1px dashed var(--border-color)",
            borderRadius: 8,
            padding: 24,
            color: "var(--text-secondary)",
            textAlign: "center",
            fontSize: 13,
          }}
        >
          {t("scheduled.empty")}
        </div>
      ) : (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 8,
            maxHeight: "56vh",
            overflow: "auto",
          }}
        >
          {tasks.map((task) => (
            <div
              key={task.id}
              style={{
                border: "1px solid var(--border-color)",
                borderRadius: 8,
                padding: 12,
                background: "var(--content-bg, #fff)",
              }}
            >
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  gap: 12,
                }}
              >
                <button
                  type="button"
                  onClick={() => startEdit(task)}
                  style={{
                    border: "none",
                    background: "transparent",
                    padding: 0,
                    color: "var(--text-primary)",
                    textAlign: "left",
                    cursor: "pointer",
                    minWidth: 0,
                    flex: 1,
                  }}
                >
                  <div
                    style={{
                      fontSize: 14,
                      fontWeight: 700,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {task.name || t("scheduled.unnamedTask")}
                  </div>
                </button>
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 4,
                    flexShrink: 0,
                  }}
                >
                  <button
                    type="button"
                    title={t("scheduled.stop")}
                    aria-label={t("scheduled.stopTask")}
                    disabled={!task.enabled}
                    onClick={() => {
                      if (task.enabled) void toggleEnabled(task);
                    }}
                    style={{
                      ...taskIconButtonStyle,
                      color: task.enabled ? "#dc2626" : "var(--text-secondary)",
                      opacity: task.enabled ? 1 : 0.5,
                      cursor: task.enabled ? "pointer" : "not-allowed",
                    }}
                  >
                    <StopIcon />
                  </button>
                  <button
                    type="button"
                    title={t("scheduled.runNow")}
                    aria-label={t("scheduled.runNowTask")}
                    disabled={runningTaskId === task.id}
                    onClick={() => void runNow(task)}
                    style={{
                      ...taskIconButtonStyle,
                      color: "var(--accent-color)",
                      opacity: runningTaskId === task.id ? 0.6 : 1,
                    }}
                  >
                    <RunNowIcon />
                  </button>
                  <button
                    type="button"
                    title={t("common.edit")}
                    aria-label={t("scheduled.editTask")}
                    onClick={() => startEdit(task)}
                    style={taskIconButtonStyle}
                  >
                    {renderToolIcon("edit")}
                  </button>
                  <button
                    type="button"
                    title={t("common.delete")}
                    aria-label={t("scheduled.deleteTask")}
                    onClick={() => void remove(task)}
                    style={{ ...taskIconButtonStyle, color: "#dc2626" }}
                  >
                    <DeleteIcon />
                  </button>
                </div>
              </div>
              <div
                style={{
                  marginTop: 6,
                  display: "grid",
                  gridTemplateColumns: "1fr 1fr",
                  gap: 8,
                  fontSize: 12,
                  color: "var(--text-secondary)",
                }}
              >
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 8,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  <span>{task.enabled ? t("scheduled.enabled") : t("scheduled.disabled")}</span>
                  <span>{task.task_cron}</span>
                  {task.new_session_cron ? (
                    <span>{t("scheduled.newSessionCron", { cron: task.new_session_cron })}</span>
                  ) : null}
                  {task.running ? (
                    <span style={{ color: "var(--accent-color)" }}>{t("scheduled.running")}</span>
                  ) : null}
                </span>
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 4,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  <AgentIcon agentName={task.agent} size={14} />
                  <span>{task.agent}</span>
                </span>
              </div>
              <div
                style={{
                  marginTop: 10,
                  display: "grid",
                  gridTemplateColumns: "1fr 1fr",
                  gap: 8,
                  fontSize: 12,
                  color: "var(--text-secondary)",
                }}
              >
                <span>{t("scheduled.lastRun", { time: formatTime(task.last_run_at, locale, t("scheduled.neverRun")) })}</span>
                <span>{t("scheduled.nextRun", { time: formatTime(task.next_run_at, locale, t("scheduled.neverRun")) })}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </>
  );

  const renderForm = () => (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <label
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 6,
          fontSize: 12,
          color: "var(--text-secondary)",
        }}
      >
        {t("scheduled.taskName")}
        <input
          className="scheduled-agent-task-input"
          value={form.name}
          onChange={(event) =>
            setForm((prev) => ({ ...prev, name: event.target.value }))
          }
          placeholder={t("scheduled.taskNamePlaceholder")}
          required
          style={fieldStyle}
        />
      </label>
      <CronEditor
        label={t("scheduled.taskCron")}
        value={form.task_cron}
        onChange={(value) => setForm((prev) => ({ ...prev, task_cron: value }))}
        placeholder="0 9 * * 1-5"
      />
      <CronEditor
        label={t("scheduled.newSessionPlan")}
        value={form.new_session_cron}
        onChange={(value) =>
          setForm((prev) => ({ ...prev, new_session_cron: value }))
        }
        placeholder="     "
        allowEmpty
        headerRight={
          <>
            <button
              type="button"
              onClick={() =>
                setForm((prev) => ({
                  ...prev,
                  new_session_cron: prev.task_cron,
                }))
              }
              style={strategyButtonStyle}
            >
              {t("scheduled.alwaysNewSession")}
            </button>
            <button
              type="button"
              onClick={() =>
                setForm((prev) => ({ ...prev, new_session_cron: "" }))
              }
              style={strategyButtonStyle}
            >
              {t("scheduled.alwaysReuseSession")}
            </button>
          </>
        }
      />
      <label
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 6,
          fontSize: 12,
          color: "var(--text-secondary)",
        }}
      >
        {t("scheduled.prompt")}
        <textarea
          className="scheduled-agent-task-input"
          value={form.prompt}
          onChange={(event) =>
            setForm((prev) => ({ ...prev, prompt: event.target.value }))
          }
          rows={4}
          style={{ ...fieldStyle, resize: "vertical", lineHeight: 1.5 }}
        />
      </label>
      <div
        style={{
          display: "flex",
          justifyContent: "flex-end",
          gap: 8,
          paddingTop: 4,
        }}
      >
        <button
          type="button"
          onClick={() => setView("list")}
          style={menuButtonStyle}
        >
          {t("common.cancel")}
        </button>
        <button
          type="button"
          onClick={() => void save()}
          disabled={saving}
          style={{
            ...menuButtonStyle,
            background: "var(--accent-color)",
            color: "#fff",
            borderColor: "var(--accent-color)",
            opacity: saving ? 0.65 : 1,
          }}
        >
          {saving ? t("common.saving") : t("common.save")}
        </button>
      </div>
    </div>
  );

  return (
    <div
      className="scheduled-agent-task-overlay"
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 1000,
        background: "rgba(15, 23, 42, 0.32)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 16,
      }}
      onMouseDown={onClose}
    >
      <div
        className="scheduled-agent-task-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={t("scheduled.title")}
        onMouseDown={(event) => event.stopPropagation()}
        style={{
          width: "min(760px, 100%)",
          maxHeight: "86vh",
          overflow: "hidden",
          borderRadius: 10,
          border: "1px solid var(--border-color)",
          background: "var(--menu-bg)",
          boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)",
          display: "flex",
          flexDirection: "column",
        }}
      >
        <style>{`
	          .scheduled-agent-task-input:focus {
	            border-color: var(--accent-color) !important;
            box-shadow: none;
          }
            @media (max-width: 640px) {
              .scheduled-agent-task-overlay {
                top: 36px !important;
                align-items: flex-start !important;
                padding: 8px 12px 12px !important;
              }
              .scheduled-agent-task-dialog {
                width: 100% !important;
                max-height: calc(100dvh - 56px) !important;
              }
              .scheduled-agent-task-dialog-body {
                -webkit-overflow-scrolling: touch;
              }
            }
        `}</style>
        <div
          style={{
            height: 48,
            padding: "0 16px",
            borderBottom: "1px solid var(--border-color)",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            flexShrink: 0,
          }}
        >
          <div style={{ fontSize: 15, fontWeight: 700 }}>
            {view === "list"
              ? t("scheduled.title")
              : view === "create"
                ? t("scheduled.createTitle")
                : t("scheduled.editTitle")}
          </div>
          {view === "list" ? (
            <button
              type="button"
              onClick={startCreate}
              style={{
                ...menuButtonStyle,
                background: "var(--accent-color)",
                color: "#fff",
                borderColor: "var(--accent-color)",
              }}
            >
              {t("scheduled.create")}
            </button>
          ) : (
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <AgentSelector
                agent={form.agent}
                model={form.model}
                mode={form.mode}
                effort={form.effort}
                fastService={form.fast_service}
                agents={agents}
                compact
                menuPlacement="bottom"
                showChevron
                onAgentChange={(agent, model) => {
                  const status = agents.find((item) => item.name === agent) || null;
                  const nextBaseModel = String(model || "").trim();
                  const nextModelInfo = status?.models?.find(
                    (item) => item.id === nextBaseModel || strip1MSuffix(item.id) === strip1MSuffix(nextBaseModel),
                  );
                  const nextAvailableEfforts = nextModelInfo?.efforts ?? status?.efforts ?? [];
                  setForm((prev) => ({
                    ...prev,
                    agent,
                    // form.model 会被原样 POST 出去（见提交处），所以这里丢了 [1m] 是真的
                    // 影响执行，不只是显示。下拉回传裸 id，须按继承规则补回后缀。
                    model: with1MSuffix(
                      nextBaseModel,
                      resolveLongContextOnSwitch({
                        nextAgent: agent,
                        nextModel: nextBaseModel,
                        prevLongContext: has1MSuffix(form.model),
                      }),
                    ),
                    mode: "",
                    effort: resolveEffortOnSwitch({
                      nextAgent: agent,
                      nextModel: nextBaseModel,
                      prevEffort: form.effort,
                      defaultEffort: status?.default_effort || "",
                      availableEfforts: nextAvailableEfforts,
                    }),
                    fast_service: "",
                  }));
                }}
                onModeChange={(mode) =>
                  setForm((prev) => ({ ...prev, mode: mode || "" }))
                }
                onEffortChange={(effort) =>
                  setForm((prev) => ({ ...prev, effort: effort || "" }))
                }
                onFastServiceChange={(fastService) =>
                  setForm((prev) => ({
                    ...prev,
                    fast_service: fastService || "",
                  }))
                }
              />
              <label
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  fontSize: 13,
                  color: "var(--text-primary)",
                }}
              >
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(event) =>
                    setForm((prev) => ({
                      ...prev,
                      enabled: event.target.checked,
                    }))
                  }
                />
                {t("scheduled.enable")}
              </label>
            </div>
          )}
        </div>
        <div
          className="scheduled-agent-task-dialog-body"
          data-dialog-view={view === "list" ? "list" : "form"}
          ref={dialogBodyRef}
          style={{
            padding: "16px 16px max(16px, env(safe-area-inset-bottom))",
            overflow: "auto",
            overscrollBehavior: "contain",
            flex: 1,
            minHeight: 0,
          }}
        >
          {error ? (
            <div
              style={{
                marginBottom: 12,
                border: "1px solid #fecaca",
                borderRadius: 8,
                background: "#fef2f2",
                color: "#b91c1c",
                padding: "8px 10px",
                fontSize: 12,
              }}
            >
              {error}
            </div>
          ) : null}
          {view === "list" ? renderList() : renderForm()}
        </div>
      </div>
    </div>
  );
}

import React from "react";
import { AgentSelector } from "../agent/AgentSelector";
import { has1MSuffix, resolveEffortOnSwitch, resolveLongContextOnSwitch, strip1MSuffix, with1MSuffix } from "../action/modelUtils";
import { useI18n } from "../../i18n/index";
import {
  CronEditor,
  fieldStyle,
} from "./ScheduledCronEditor";
import { useScheduledAgentTaskDialog } from "./useScheduledAgentTaskDialog";
import { menuButtonStyle, strategyButtonStyle } from "../../shared/styles";
import type { AgentStatus } from "../../shared/agentTypes";
import { ScheduledAgentTaskList } from "./ScheduledAgentTaskList";

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
  const {
    view,
    setView,
    tasks,
    form,
    setForm,
    loading,
    saving,
    error,
    runningTaskId,
    dialogBodyRef,
    startCreate,
    startEdit,
    save,
    remove,
    runNow,
    toggleEnabled,
  } = useScheduledAgentTaskDialog({ open, rootId, agents });

  if (!open) return null;

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
          {view === "list" ? (
            <ScheduledAgentTaskList
              tasks={tasks}
              loading={loading}
              runningTaskId={runningTaskId}
              locale={locale}
              onToggleEnabled={(task) => { if (task.enabled) void toggleEnabled(task); }}
              onRunNow={(task) => { void runNow(task); }}
              onEdit={startEdit}
              onDelete={(task) => { void remove(task); }}
            />
          ) : renderForm()}
        </div>
      </div>
    </div>
  );
}

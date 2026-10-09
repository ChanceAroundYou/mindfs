import { useEffect, useMemo, useRef, useState } from "react";
import {
  createScheduledAgentTask,
  deleteScheduledAgentTask,
  fetchScheduledAgentTasks,
  runScheduledAgentTask,
  updateScheduledAgentTask,
  type ScheduledAgentTask,
} from "../../services/scheduledTasks";
import { useI18n } from "../../i18n/index";
import { confirmDialog } from "../../services/dialog";
import { emptyForm, taskToForm, type FormState } from "./scheduledTaskUtils";
import type { AgentStatus } from "../../services/agents";

type DialogView = "list" | "create" | "edit";

export function useScheduledAgentTaskDialog({
  open,
  rootId,
  agents,
}: {
  open: boolean;
  rootId?: string | null;
  agents: AgentStatus[];
}) {
  const { t } = useI18n();
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

  return {
    view,
    setView,
    tasks,
    selected,
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
  };
}

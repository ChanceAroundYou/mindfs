import { useCallback, useEffect, useRef, useState } from "react";
import {
  deleteTaskTemplate,
  fetchTaskTemplates,
  saveTaskTemplate,
  type TaskTemplate,
} from "../services/tasks";
import { useI18n } from "../i18n";
import { protectedAPIReady } from "../services/api";
import { reportError } from "../services/error";
import { TASK_TEMPLATE_ALL_FILTER, TASK_TEMPLATE_SELECTION_STORAGE_KEY } from "./appStorage";
import { confirmDialog } from "../services/dialog";

/**
 * 任务模板：列表 + 增删改 + 「当前项目选中的模板」记忆。
 * 选中项按项目存 localStorage（切项目别串味），模板菜单的互斥/点外关闭也归这里。
 */
export function useTaskTemplates({
  currentRootId,
  scopedRootKey,
  getNodeIdForRoot,
}: {
  currentRootId: string | null;
  scopedRootKey: (rootId: string) => string;
  getNodeIdForRoot: (rootId: string) => string | undefined;
}) {
  const { t } = useI18n();
  const taskTemplateActionMenuRef = useRef<HTMLDivElement | null>(null);
  const taskCreateTemplateMenuRef = useRef<HTMLDivElement | null>(null);

  const [taskTemplates, setTaskTemplates] = useState<TaskTemplate[]>([]);
  const [taskTemplateDialogOpen, setTaskTemplateDialogOpen] = useState(false);
  const [taskTemplateDialogTemplate, setTaskTemplateDialogTemplate] = useState<TaskTemplate | null>(null);
  const [taskTemplateFilter, setTaskTemplateFilter] = useState("");
  const [taskTemplateActionMenuOpen, setTaskTemplateActionMenuOpen] = useState(false);
  const [taskCreateTemplateMenuOpen, setTaskCreateTemplateMenuOpen] = useState(false);

  // 模板请求必须带上当前项目的 nodeId。不带的话 getApiBaseURL(undefined) 会
  // 回退到 active node —— 面板明明开着本机项目，请求却打到另一台机器上去了
  // （实测：https://pc.xiaokubao.space/... 而页面在 127.0.0.1）。
  const templateNodeId = useCallback((): string | undefined => (
    currentRootId ? getNodeIdForRoot(currentRootId) : undefined
  ), [currentRootId, getNodeIdForRoot]);

  const loadTaskTemplates = useCallback(async () => {
    if (!protectedAPIReady()) {
      return;
    }
    try {
      setTaskTemplates(await fetchTaskTemplates(templateNodeId()));
    } catch (err) {
      reportError("file.write_failed", String((err as Error)?.message || t("taskTemplate.loadFailed")));
    }
  }, [t, templateNodeId]);

  const openTaskTemplateEditor = useCallback((template: TaskTemplate | null) => {
    setTaskTemplateDialogTemplate(template);
    setTaskTemplateDialogOpen(true);
  }, []);

  const handleTaskTemplateSaved = useCallback((template: TaskTemplate) => {
    setTaskTemplates((prev) => {
      const id = template.id || "";
      if (!id) return prev;
      const index = prev.findIndex((item) => item.id === id);
      if (index < 0) return [template, ...prev];
      const next = [...prev];
      next[index] = template;
      return next;
    });
    setTaskTemplateDialogTemplate(template);
  }, []);

  const handleDeleteTaskTemplate = useCallback(async (template: TaskTemplate) => {
    const id = template.id || "";
    if (!id) return;
    if (!await confirmDialog({ message: t("taskTemplate.deleteConfirm", { name: template.name || id }), danger: true })) return;
    try {
      await deleteTaskTemplate(id, templateNodeId());
      setTaskTemplates((prev) => prev.filter((item) => item.id !== id));
      setTaskTemplateDialogTemplate((prev) => prev?.id === id ? null : prev);
      setTaskTemplateFilter((prev) => prev === id ? "" : prev);
    } catch (err) {
      reportError("file.write_failed", String((err as Error)?.message || t("taskTemplate.deleteFailed")));
    }
  }, [t]);


  useEffect(() => {
    void loadTaskTemplates();
  }, [loadTaskTemplates]);

  // 切项目：若当前选中项不属于本项目，回落到该项目记住的模板（再回落到「全部」）
  useEffect(() => {
    if (!currentRootId) {
      if (taskTemplateFilter) setTaskTemplateFilter("");
      return;
    }
    if (taskTemplateFilter === TASK_TEMPLATE_ALL_FILTER || (taskTemplateFilter && taskTemplates.some((template) => template.id === taskTemplateFilter))) {
      return;
    }
    let remembered = "";
    try {
      const parsed = JSON.parse(window.localStorage.getItem(TASK_TEMPLATE_SELECTION_STORAGE_KEY) || "{}") as Record<string, unknown>;
      const value = parsed[scopedRootKey(currentRootId)];
      remembered = typeof value === "string" ? value : "";
    } catch {
      remembered = "";
    }
    const rememberedValid = remembered === TASK_TEMPLATE_ALL_FILTER || taskTemplates.some((template) => template.id === remembered);
    const next = rememberedValid ? remembered : TASK_TEMPLATE_ALL_FILTER;
    if (next && next !== taskTemplateFilter) {
      setTaskTemplateFilter(next);
    }
  }, [currentRootId, taskTemplateFilter, taskTemplates, scopedRootKey]);

  useEffect(() => {
    if (!currentRootId || !taskTemplateFilter) return;
    try {
      const parsed = JSON.parse(window.localStorage.getItem(TASK_TEMPLATE_SELECTION_STORAGE_KEY) || "{}") as Record<string, unknown>;
      window.localStorage.setItem(TASK_TEMPLATE_SELECTION_STORAGE_KEY, JSON.stringify({
        ...parsed,
        [scopedRootKey(currentRootId)]: taskTemplateFilter,
      }));
    } catch {
    }
  }, [currentRootId, taskTemplateFilter, scopedRootKey]);

  useEffect(() => {
    if (!taskTemplateActionMenuOpen) return;
    const onPointerDown = (event: MouseEvent) => {
      if (taskTemplateActionMenuRef.current?.contains(event.target as Node)) {
        return;
      }
      setTaskTemplateActionMenuOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    return () => document.removeEventListener("mousedown", onPointerDown);
  }, [taskTemplateActionMenuOpen]);

  useEffect(() => {
    if (!taskCreateTemplateMenuOpen) return;
    const onPointerDown = (event: MouseEvent) => {
      if (taskCreateTemplateMenuRef.current?.contains(event.target as Node)) {
        return;
      }
      setTaskCreateTemplateMenuOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    return () => document.removeEventListener("mousedown", onPointerDown);
  }, [taskCreateTemplateMenuOpen]);

  useEffect(() => {
    if (taskTemplateActionMenuOpen) {
      setTaskCreateTemplateMenuOpen(false);
    }
  }, [taskTemplateActionMenuOpen]);

  return {
    taskTemplateActionMenuRef,
    taskCreateTemplateMenuRef,
    taskTemplates,
    taskTemplateDialogOpen,
    setTaskTemplateDialogOpen,
    taskTemplateDialogTemplate,
    taskTemplateFilter,
    setTaskTemplateFilter,
    taskTemplateActionMenuOpen,
    setTaskTemplateActionMenuOpen,
    taskCreateTemplateMenuOpen,
    setTaskCreateTemplateMenuOpen,
    loadTaskTemplates,
    openTaskTemplateEditor,
    handleTaskTemplateSaved,
    handleDeleteTaskTemplate,
  };
}

import { useState, type Dispatch, type SetStateAction } from "react";
import { saveTaskTemplate } from "../services/tasks";
import { reportError } from "../services/net/error";
import type { I18nContextValue } from "../i18n/index";
import type { StageTemplate, TaskTemplate, TaskTemplateStage } from "../shared/taskTypes";

export function useTaskTemplateSave({
  draft,
  setDraft,
  scopeToCurrentProject,
  currentRootId,
  onSaved,
  onClose,
  t,
  normalizeStages,
  cloneTemplate,
}: {
  draft: TaskTemplate;
  setDraft: Dispatch<SetStateAction<TaskTemplate>>;
  scopeToCurrentProject: boolean;
  currentRootId?: string | null;
  onSaved?: (template: TaskTemplate) => void;
  onClose: () => void;
  t: I18nContextValue["t"];
  normalizeStages: (stages: TaskTemplateStage[]) => TaskTemplateStage[];
  cloneTemplate: (template?: TaskTemplate | null, t?: I18nContextValue["t"]) => TaskTemplate;
}) {
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");

  const saveTask = async () => {
    if (!draft.name.trim()) {
      reportError("task.action_failed", t("taskTemplate.taskTemplateNameRequired"));
      return;
    }
    if (!draft.stages[0] || draft.stages[0].snapshot.role !== "user") {
      reportError("task.action_failed", t("taskTemplate.firstStageMustBeUser"));
      return;
    }
    setSaving(true);
    setSaveError("");
    try {
      const saved = await saveTaskTemplate({
        ...draft,
        root_id: scopeToCurrentProject ? (currentRootId || "") : "",
        stages: normalizeStages(draft.stages),
      });
      setDraft(cloneTemplate(saved, t));
      onSaved?.(saved);
      onClose();
    } catch (err) {
      const message = String((err as Error)?.message || t("taskTemplate.taskTemplateSaveFailed"));
      setSaveError(message);
      reportError("task.action_failed", message);
    } finally {
      setSaving(false);
    }
  };

  return { saving, saveError, setSaveError, saveTask };
}

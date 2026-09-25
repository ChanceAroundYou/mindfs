import React, { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n";
import { Select } from "../Select";
import { PlusSmallIcon } from "../../app/taskIcons";
import type { WorkspaceProjectGroup } from "../../app/useWorkspaceBoard";
import type { TaskTemplate } from "../../services/tasks";
import { workspaceQuickLaunchMobileStyle, workspaceQuickLaunchStyle } from "./workspaceStyles";

/**
 * 快速发起：一个按钮，点开后才出「选项目 + 选模板」的面板。
 *
 * 为什么不是常驻输入框：常驻的编辑器会自己抢焦点（工作台是主区，一进来焦点就被它吃掉，
 * 键盘用户没法先 Tab 到别处）。改成按钮 + 面板后，没点它就完全不挂载编辑器。
 *
 * 面板本身不自己实现建任务 —— 它挑完项目和模板就交给看板那套新建任务面板
 * （openTaskCreateDialog），模板、worktree、agent 选择、附件全都跟项目看板里一致，
 * 不在工作台上复制一份第二实现。
 */
export function WorkspaceQuickLaunch({ projects, templates, onPick, isMobile }: {
  projects: WorkspaceProjectGroup[];
  templates: TaskTemplate[];
  onPick: (rootId: string, nodeId: string, template: TaskTemplate) => void;
  isMobile: boolean;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [rootKey, setRootKey] = useState("");
  const [templateId, setTemplateId] = useState("");
  const wrapRef = useRef<HTMLDivElement | null>(null);

  // 工作台只显示有任务的项目，所以「选哪个项目」和「看哪个项目」永远同一批。
  const options = projects.map((group) => ({ value: group.key, label: group.rootName }));
  const effectiveKey = options.some((option) => option.value === rootKey) ? rootKey : options[0]?.value || "";
  const effectiveTemplateId = templates.some((tpl) => tpl.id === templateId) ? templateId : templates[0]?.id || "";

  useEffect(() => {
    if (projects.length === 0) setRootKey("");
  }, [projects.length]);

  // 点外面收起。面板是纯本地的临时选择，Esc / 点外都该能直接丢掉。
  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  if (projects.length === 0 || templates.length === 0) return null;
  return (
    <div ref={wrapRef} style={{ position: "relative", display: "flex", justifyContent: "flex-end" }}>
      <button
        type="button"
        data-onboarding="task-create"
        title={t("task.workspaceQuickLaunch")}
        aria-label={t("task.workspaceQuickLaunch")}
        aria-expanded={open}
        onClick={() => setOpen((prev) => !prev)}
        style={{
          height: "30px",
          padding: "0 12px",
          borderRadius: "8px",
          border: "1px solid var(--border-color)",
          background: open ? "var(--node-row-selected-bg)" : "transparent",
          color: "var(--text-color)",
          fontSize: "12px",
          fontWeight: 700,
          display: "inline-flex",
          alignItems: "center",
          gap: "5px",
          cursor: "pointer",
        }}
      >
        <PlusSmallIcon />
        {t("task.workspaceQuickLaunch")}
      </button>
      {open ? (
        <div
          style={{
            position: "absolute",
            bottom: "calc(100% + 6px)",
            right: 0,
            display: "flex",
            flexDirection: "column",
            gap: "8px",
            padding: "10px",
            borderRadius: "10px",
            border: "1px solid var(--border-color)",
            background: "var(--menu-bg)",
            boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
            zIndex: 40,
            ...(isMobile ? { ...workspaceQuickLaunchStyle, ...workspaceQuickLaunchMobileStyle } : {}),
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
            <span style={{ fontSize: "12px", fontWeight: 800, color: "var(--text-secondary)", whiteSpace: "nowrap" }}>
              {t("task.workspaceSelectProject")}
            </span>
            <div style={{ flex: "1 1 auto", minWidth: 0 }}>
              <Select
                value={effectiveKey}
                onChange={setRootKey}
                options={options}
                size="panel"
                ariaLabel={t("task.workspaceSelectProject")}
              />
            </div>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
            <span style={{ fontSize: "12px", fontWeight: 800, color: "var(--text-secondary)", whiteSpace: "nowrap" }}>
              {t("task.selectTemplate")}
            </span>
            <div style={{ flex: "1 1 auto", minWidth: 0 }}>
              <Select
                value={effectiveTemplateId}
                onChange={setTemplateId}
                options={templates.map((tpl) => ({ value: tpl.id || "", label: tpl.name }))}
                size="panel"
                ariaLabel={t("task.selectTemplate")}
              />
            </div>
          </div>
          <button
            type="button"
            onClick={() => {
              const target = projects.find((group) => group.key === effectiveKey);
              const template = templates.find((tpl) => tpl.id === effectiveTemplateId);
              if (!target || !template) return;
              setOpen(false);
              onPick(target.rootId, target.nodeId, template);
            }}
            style={{
              height: "30px",
              borderRadius: "6px",
              border: "none",
              background: "var(--accent-color)",
              color: "#fff",
              fontSize: "12px",
              fontWeight: 700,
              cursor: "pointer",
            }}
          >
            {t("task.create")}
          </button>
        </div>
      ) : null}
    </div>
  );
}

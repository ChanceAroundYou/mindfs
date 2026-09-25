import React, { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../i18n";
import { PromptEditor } from "../PromptEditor";
import { Select } from "../Select";
import type { TokenEditorHandle } from "../editor/TokenEditor";
import type { WorkspaceProjectGroup } from "../../app/useWorkspaceBoard";
import { workspaceQuickLaunchMobileStyle, workspaceQuickLaunchStyle } from "./workspaceStyles";

/**
 * 快速发起：常驻工作台底部。
 *
 * 复用 PromptEditor 而不是裸 <input>（与任务侧其它输入统一：@ 补全、多行、Enter 发送），
 * 提交后必须手动 clear()：编辑器是常驻挂载的，resetKey 不变它不会重灌。
 */
export function WorkspaceQuickLaunch({ projects, onCreateTask, isMobile }: {
  projects: WorkspaceProjectGroup[];
  onCreateTask: (rootId: string, nodeId: string, input: string) => void;
  isMobile: boolean;
}) {
  const { t } = useI18n();
  const [rootKey, setRootKey] = useState("");
  const [input, setInput] = useState("");
  const editorRef = useRef<TokenEditorHandle | null>(null);

  const options = useMemo(
    () => projects.map((group) => ({ value: group.key, label: group.rootName })),
    [projects],
  );
  // 项目增删后旧选中会悬空，回落到第一个，避免「选了个不存在的项目」
  const effectiveKey = options.some((option) => option.value === rootKey) ? rootKey : options[0]?.value || "";

  useEffect(() => {
    if (projects.length === 0) setRootKey("");
  }, [projects.length]);

  const submit = () => {
    const target = projects.find((group) => group.key === effectiveKey);
    const text = input.trim();
    if (!text || !target) return;
    onCreateTask(target.rootId, target.nodeId, text);
    setInput("");
    editorRef.current?.clear();
  };

  if (projects.length === 0) return null;
  return (
    <div style={isMobile ? { ...workspaceQuickLaunchStyle, ...workspaceQuickLaunchMobileStyle } : workspaceQuickLaunchStyle}>
      <span style={{ fontSize: "12px", fontWeight: 800, color: "var(--text-secondary)", whiteSpace: "nowrap" }}>
        {t("task.workspaceQuickLaunch")}
      </span>
      <div style={isMobile ? { width: "100%" } : { minWidth: "120px", maxWidth: "180px" }}>
        <Select value={effectiveKey} onChange={setRootKey} options={options} />
      </div>
      <div style={{ flex: "1 1 260px", minWidth: "160px" }}>
        <PromptEditor
          ref={editorRef}
          value={input}
          onChange={setInput}
          resetKey="workspace-quick-launch"
          role="user"
          placeholder={t("task.quickLaunchPlaceholder")}
          onSend={submit}
          sendDisabled={!input.trim() || !effectiveKey}
        />
      </div>
    </div>
  );
}

import React from "react";
import { LOCAL_NODE_ID } from "../../shared/nodePalette";
import { useProjectAddNodes } from "./useProjectAddNodes";
import { useI18n } from "../../i18n/index";
import { PathBreadcrumb } from "./PathBreadcrumb";
import { AsyncStateMessage } from "../common/AsyncStateMessage";
import { ProjectAddModePanel } from "./ProjectAddModePanel";
import { localBareStyle, projectAddPopoverStyle } from "./projectAddStyles";
import type { GitHubImportState, LocalDirBrowserState, ProjectAddMode } from "../../shared/projectAddTypes";
export type { GitHubImportState, LocalDirBrowserState, LocalDirItem, ProjectAddMode } from "../../shared/projectAddTypes";

type ProjectAddPopoverProps = {
  mode: ProjectAddMode;
  onSelectMode: () => void;
  onSelectLocal: () => void;
  onSelectBlankLocation: () => void;
  onSelectGitHubLocation: () => void;
  onSelectGitHub: () => void;
  onSelectBlank: () => void;
  localState: LocalDirBrowserState;
  selectedNodeId?: string;
  onSelectedNodeChange?: (id: string) => void;
  onLocalNavigate: (path: string) => void;
  onLocalSelect: (path: string) => void;
  onLocalAdd: () => void;
  localActionLabel: string;
  localDisabledAddedRoot: boolean;
  localBrowseOnly: boolean;
  /**
   * 去掉自带的外框与动作按钮，只留「面包屑 + 目录列表」。
   *
   * 给把它嵌进自己弹层的调用方用（会话标题栏的「修改目录」）：那边已经有框、
   * 有确定按钮，再叠一层就是框里框、两个确定。默认 false = 新建项目那个独立气泡。
   */
  localBare?: boolean;
  githubState: GitHubImportState;
  onGitHubUrlChange: (value: string) => void;
  onGitHubImport: () => void;
};

const inputStyle: React.CSSProperties = {
  width: "100%",
  borderRadius: "8px",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "8px 10px",
  outline: "none",
  boxSizing: "border-box",
};

export function LocalPanel({
  localState,
  selectedNodeId,
  onSelectedNodeChange,
  onLocalNavigate,
  onLocalSelect,
  onLocalAdd,
  localActionLabel,
  localDisabledAddedRoot,
  localBrowseOnly,
  localBare,
}: Pick<
  ProjectAddPopoverProps,
  | "localState"
  | "selectedNodeId"
  | "onSelectedNodeChange"
  | "onLocalNavigate"
  | "onLocalSelect"
  | "onLocalAdd"
  | "localActionLabel"
  | "localDisabledAddedRoot"
  | "localBrowseOnly"
  | "localBare"
>) {
  const { t } = useI18n();
  const actionDisabled =
    localState.loading ||
    !!localState.error ||
    localState.adding ||
    (!localBrowseOnly && !localState.selectedPath);
  const actionBackground = !actionDisabled
    ? "var(--accent-color)"
    : "color-mix(in srgb, var(--accent-color) 45%, transparent)";
  const actionCursor = !actionDisabled ? "pointer" : "not-allowed";
  const volumes = Array.isArray(localState.volumes) ? localState.volumes : [];

  const nodes = useProjectAddNodes();
  return (
    <div style={localBare ? localBareStyle : projectAddPopoverStyle}>
      {nodes.length > 1 && onSelectedNodeChange ? (
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", paddingBottom: 6, borderBottom: "1px solid var(--border-color)" }}>
          {nodes.map((n) => {
            const active = (selectedNodeId || LOCAL_NODE_ID) === n.id;
            return (
              <button key={n.id} type="button" onClick={() => onSelectedNodeChange(n.id)} style={{ display: "inline-flex", alignItems: "center", gap: 6, border: active ? "1px solid var(--accent-color)" : "1px solid var(--border-color)", background: active ? "var(--selection-bg)" : "transparent", color: active ? "var(--accent-color)" : "var(--text-primary)", borderRadius: 999, padding: "4px 8px", fontSize: 12, cursor: "pointer" }}>
                <span style={{ width: 8, height: 8, borderRadius: "50%", background: n.color, display: "inline-block" }} />
                <span style={{ fontWeight: active ? 700 : 500 }}>{n.name}</span>
              </button>
            );
          })}
        </div>
      ) : null}
      <div style={{ display: "flex", alignItems: "center", gap: 4, minWidth: 0 }}>
        <button
          type="button"
          disabled={!localState.parent}
          onClick={() => onLocalNavigate(localState.parent || "")}
          aria-label={t("projectAdd.goUp")}
          title={t("projectAdd.goUp")}
          style={{
            border: "none",
            background: "transparent",
            padding: 0,
            color: "var(--text-secondary)",
            cursor: localState.parent ? "pointer" : "default",
            opacity: localState.parent ? 1 : 0.35,
            display: "inline-flex",
            alignItems: "center",
            flexShrink: 0,
          }}
        >
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <polyline points="18 15 12 9 6 15" />
          </svg>
        </button>
        <PathBreadcrumb
          path={localState.path}
          volumes={volumes}
          onNavigate={onLocalNavigate}
        />
      </div>
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          maxHeight: "240px",
          overflow: "auto",
        }}
      >
        {localState.loading ? (
          <AsyncStateMessage kind="loading" style={{ padding: "8px 10px" }}>
            {t("projectAdd.loading")}
          </AsyncStateMessage>
        ) : null}
        {!localState.loading && localState.error ? (
          <AsyncStateMessage kind="error" style={{ padding: "8px 10px" }}>
            {localState.error}
          </AsyncStateMessage>
        ) : null}
        {!localState.loading &&
        !localState.error &&
        localState.items.length === 0 ? (
          <AsyncStateMessage kind="empty" style={{ padding: "8px 10px" }}>
            {t("projectAdd.emptyDirectory")}
          </AsyncStateMessage>
        ) : null}
        {!localState.loading &&
          !localState.error &&
          localState.items.map((item) => {
            const selected = localState.selectedPath === item.path;
            const disabled =
              localDisabledAddedRoot && item.is_added_root === true;
            return (
              <div
                key={item.path}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "6px",
                  width: "100%",
                  borderRadius: "8px",
                }}
              >
                <button
                  type="button"
                  disabled={disabled || localBrowseOnly}
                  onClick={() => onLocalSelect(item.path)}
                  style={{
                    flex: 1,
                    minWidth: 0,
                    border: "1px solid transparent",
                    background:
                      !localBrowseOnly && selected
                        ? "var(--selection-bg)"
                        : "transparent",
                    color: disabled
                      ? "var(--text-secondary)"
                      : !localBrowseOnly && selected
                        ? "var(--accent-color)"
                        : "var(--text-primary)",
                    opacity: disabled ? 0.55 : 1,
                    borderRadius: "8px",
                    padding: "8px 10px",
                    cursor:
                      disabled
                        ? "not-allowed"
                        : localBrowseOnly
                          ? "default"
                          : "pointer",
                    textAlign: "left",
                    fontSize: "12px",
                    fontWeight:
                      !localBrowseOnly && selected ? 600 : 400,
                  }}
                >
                  <span style={{ display: "block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {item.name}
                  </span>
                </button>
                <button
                  type="button"
                  onClick={() => onLocalNavigate(item.path)}
                  style={{
                    width: "28px",
                    height: "28px",
                    border: "none",
                    background: "transparent",
                    color: "var(--text-secondary)",
                    borderRadius: "8px",
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    cursor: "pointer",
                    flexShrink: 0,
                  }}
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <polyline points="9 18 15 12 9 6" />
                  </svg>
                </button>
              </div>
            );
          })}
      </div>
      {localBare ? null : (
        <button
          type="button"
          disabled={actionDisabled}
          onClick={onLocalAdd}
          style={{
            border: "none",
            background: actionBackground,
            color: "#fff",
            borderRadius: "8px",
            padding: "9px 10px",
            fontSize: "12px",
            fontWeight: 600,
            opacity: 1,
            cursor: actionCursor,
          }}
        >
          {localState.adding ? t("projectAdd.processing") : localActionLabel}
        </button>
      )}
    </div>
  );
}

function GitHubPanel({
  githubState,
  onGitHubUrlChange,
  onGitHubImport,
}: Pick<
  ProjectAddPopoverProps,
  "githubState" | "onGitHubUrlChange" | "onGitHubImport"
>) {
  const { t } = useI18n();
  const disabled = !githubState.url.trim() || githubState.running || githubState.submitting;
  return (
    <div style={projectAddPopoverStyle}>
      <div style={{ fontSize: "12px", fontWeight: 600, color: "var(--text-primary)" }}>
        GitHub
      </div>
      <input
        value={githubState.url}
        onChange={(event) => onGitHubUrlChange(event.target.value)}
        placeholder="https://github.com/owner/repo"
        style={inputStyle}
      />
      {githubState.error ? (
        <div style={{ fontSize: "12px", color: "#b45309" }}>{githubState.error}</div>
      ) : null}
      <button
        type="button"
        disabled={disabled}
        onClick={onGitHubImport}
        style={{
          border: "none",
          background: disabled ? "color-mix(in srgb, var(--accent-color) 65%, transparent)" : "var(--accent-color)",
          color: "#fff",
          borderRadius: "8px",
          padding: "9px 10px",
          fontSize: "12px",
          fontWeight: 600,
          cursor: disabled ? "not-allowed" : "pointer",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          gap: "8px",
        }}
      >
        {githubState.running || githubState.submitting ? (
          <span
            style={{
              width: "12px",
              height: "12px",
              borderRadius: "50%",
              border: "2px solid currentColor",
              borderRightColor: "transparent",
              display: "inline-block",
              animation: "mindfs-update-spin 0.9s linear infinite",
            }}
          />
        ) : null}
        <span>
          {githubState.running || githubState.submitting
            ? t("projectAdd.cloning")
            : t("projectAdd.import")}
        </span>
      </button>
    </div>
  );
}

export function ProjectAddPopover({
  mode,
  onSelectMode,
  onSelectLocal,
  onSelectBlankLocation,
  onSelectGitHubLocation,
  onSelectGitHub,
  onSelectBlank,
  localState,
  selectedNodeId,
  onSelectedNodeChange,
  onLocalNavigate,
  onLocalSelect,
  onLocalAdd,
  localActionLabel,
  localDisabledAddedRoot,
  localBrowseOnly,
  githubState,
  onGitHubUrlChange,
  onGitHubImport,
}: ProjectAddPopoverProps) {
  if (mode === "mode") {
    return (
      <ProjectAddModePanel
        onSelectBlankLocation={onSelectBlankLocation}
        onSelectLocal={onSelectLocal}
        onSelectGitHubLocation={onSelectGitHubLocation}
      />
    );
  }
  if (mode === "local" || mode === "blank_location" || mode === "github_location" || mode === "worktree_location") {
    return (
      <LocalPanel
        localState={localState}
        selectedNodeId={selectedNodeId}
        onSelectedNodeChange={onSelectedNodeChange}
        onLocalNavigate={onLocalNavigate}
        onLocalSelect={onLocalSelect}
        onLocalAdd={onLocalAdd}
        localActionLabel={localActionLabel}
        localDisabledAddedRoot={localDisabledAddedRoot}
        localBrowseOnly={localBrowseOnly}
      />
    );
  }
  return (
    <GitHubPanel
      githubState={githubState}
      onGitHubUrlChange={onGitHubUrlChange}
      onGitHubImport={onGitHubImport}
    />
  );
}

export function ProjectAddLifecycleOverlay({
  popoverRef,
  mode,
  setMode,
  onSelectLocal,
  onSelectBlankLocation,
  onSelectGitHubLocation,
  onSelectBlank,
  localState,
  selectedNodeId,
  onSelectedNodeChange,
  onLocalNavigate,
  onLocalSelect,
  onLocalAdd,
  githubState,
  onGitHubUrlChange,
  onGitHubImport,
}: {
  popoverRef: React.RefObject<HTMLDivElement | null>;
  mode: ProjectAddMode;
  setMode: (mode: ProjectAddMode | null) => void;
  onSelectLocal: () => void;
  onSelectBlankLocation: () => void;
  onSelectGitHubLocation: () => void;
  onSelectBlank: () => void;
  localState: LocalDirBrowserState;
  selectedNodeId: string;
  onSelectedNodeChange: (id: string) => void;
  onLocalNavigate: (path: string) => void;
  onLocalSelect: (path: string) => void;
  onLocalAdd: () => void;
  githubState: GitHubImportState;
  onGitHubUrlChange: (value: string) => void;
  onGitHubImport: () => void;
}) {
  const { t } = useI18n();
  return (
    <div ref={popoverRef}>
      <ProjectAddPopover
        mode={mode}
        onSelectMode={() => setMode("mode")}
        onSelectLocal={onSelectLocal}
        onSelectBlankLocation={onSelectBlankLocation}
        onSelectGitHubLocation={onSelectGitHubLocation}
        onSelectGitHub={onSelectGitHubLocation}
        onSelectBlank={onSelectBlank}
        localState={localState}
        selectedNodeId={selectedNodeId}
        onSelectedNodeChange={onSelectedNodeChange}
        onLocalNavigate={onLocalNavigate}
        onLocalSelect={onLocalSelect}
        onLocalAdd={onLocalAdd}
        localActionLabel={mode === "local" ? t("projectAdd.add") : t("projectAdd.placeHere")}
        localDisabledAddedRoot={mode === "local"}
        localBrowseOnly={mode === "blank_location" || mode === "github_location" || mode === "worktree_location"}
        githubState={githubState}
        onGitHubUrlChange={onGitHubUrlChange}
        onGitHubImport={onGitHubImport}
      />
    </div>
  );
}

import React from "react";
import { getNodes, LOCAL_NODE_ID } from "../../services/net/nodeRegistry";
import { useI18n } from "../../i18n/index";
import { PathBreadcrumb } from "./PathBreadcrumb";
import { AsyncStateMessage } from "../common/AsyncStateMessage";
import type { GitHubImportState, ProjectAddMode } from "../../shared/projectAddTypes";
export type { GitHubImportState, ProjectAddMode } from "../../shared/projectAddTypes";

export type LocalDirItem = {
  name: string;
  path: string;
  is_dir: boolean;
  is_added_root?: boolean;
  root_id?: string;
};

export type LocalDirBrowserState = {
  path: string;
  parent?: string;
  volumes?: LocalDirItem[];
  items: LocalDirItem[];
  loading: boolean;
  selectedPath: string;
  adding: boolean;
  error: string;
};

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
  githubState: GitHubImportState;
  onGitHubUrlChange: (value: string) => void;
  onGitHubImport: () => void;
};

const popoverStyle: React.CSSProperties = {
  width: "248px",
  maxWidth: "calc(100vw - 32px)",
  padding: "10px",
  borderRadius: "12px",
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
  display: "flex",
  flexDirection: "column",
  gap: "10px",
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

function ModeItem({
  label,
  icon,
  iconColor,
  onClick,
}: {
  label: string;
  icon: React.ReactNode;
  iconColor?: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        display: "flex",
        alignItems: "center",
        gap: "8px",
        width: "100%",
        border: "1px solid transparent",
        background: "transparent",
        color: "var(--text-primary)",
        borderRadius: "8px",
        padding: "8px 10px",
        cursor: "pointer",
        textAlign: "left",
        fontSize: "12px",
      }}
    >
      <span
        style={{
          width: "18px",
          height: "18px",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          color: iconColor || "var(--text-secondary)",
          flexShrink: 0,
        }}
      >
        {icon}
      </span>
      <span>{label}</span>
    </button>
  );
}

function ModePanel({
  onSelectBlankLocation,
  onSelectLocal,
  onSelectGitHubLocation,
}: Pick<
  ProjectAddPopoverProps,
  "onSelectBlankLocation" | "onSelectLocal" | "onSelectGitHubLocation"
>) {
  const { t } = useI18n();
  return (
    <div style={popoverStyle}>
      <ModeItem
        label={t("projectAdd.blankProject")}
        onClick={onSelectBlankLocation}
        iconColor="var(--accent-color)"
        icon={
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round">
            <path d="M12 5v14" />
            <path d="M5 12h14" />
          </svg>
        }
      />
      <ModeItem
        label={t("projectAdd.localDirectory")}
        onClick={onSelectLocal}
        iconColor="var(--accent-color)"
        icon={
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M3 6h5l2 2h11" />
            <path d="M3 6v11a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V8H10L8 6H5a2 2 0 0 0-2 2" />
          </svg>
        }
      />
      <ModeItem
        label={t("projectAdd.githubImport")}
        onClick={onSelectGitHubLocation}
        icon={
          <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 2A10 10 0 0 0 2 12c0 4.42 2.87 8.17 6.84 9.5c.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34c-.46-1.16-1.11-1.47-1.11-1.47c-.91-.62.07-.6.07-.6c1 .07 1.53 1.03 1.53 1.03c.87 1.52 2.34 1.07 2.91.83c.09-.65.35-1.09.63-1.34c-2.22-.25-4.55-1.11-4.55-4.92c0-1.11.38-2 1.03-2.71c-.1-.25-.45-1.29.1-2.64c0 0 .84-.27 2.75 1.02c.79-.22 1.65-.33 2.5-.33s1.71.11 2.5.33c1.91-1.29 2.75-1.02 2.75-1.02c.55 1.35.2 2.39.1 2.64c.65.71 1.03 1.6 1.03 2.71c0 3.82-2.34 4.66-4.57 4.91c.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0 0 12 2" />
          </svg>
        }
      />
    </div>
  );
}

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

  const nodes = getNodes();
  return (
    <div style={popoverStyle}>
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
    <div style={popoverStyle}>
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
      <ModePanel
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

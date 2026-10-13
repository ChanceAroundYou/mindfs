import React from "react";
import type { GitBranchesPayload, GitWorktreeItem } from "../../services/git";
import { Select } from "../common/Select";
import { useI18n } from "../../i18n";

type Props = {
  mode: "create" | "switch";
  createPopoverRef: React.RefObject<HTMLDivElement | null>;
  switchPopoverRef: React.RefObject<HTMLDivElement | null>;
  creatingRootKind: "root" | "worktree";
  creatingRootName: string | null;
  creatingRootBusy: boolean;
  setCreatingRootName: (name: string) => void;
  handleCreateRootSubmit: () => Promise<void>;
  handleCreateRootCancel: () => void;
  worktreeBranches: GitBranchesPayload;
  worktreeBranchesLoading: boolean;
  worktreeBranchError: string;
  worktreeBranchMode: "new" | "existing";
  setWorktreeBranchMode: (mode: "new" | "existing") => void;
  worktreeBranch: string;
  setWorktreeBranch: (branch: string) => void;
  worktreeSwitchOpen: boolean;
  worktreeSwitchLoading: boolean;
  worktreeSwitchError: string;
  worktreeSwitchItems: GitWorktreeItem[];
  switchingWorktreePath: string;
  currentRootId: string | null;
  findManagedRootByPath: (path: string) => { id?: string } | null;
  handleSwitchWorktree: (item: GitWorktreeItem) => Promise<string | undefined>;
};

export function WorktreeLifecycleOverlays({
  mode,
  createPopoverRef,
  switchPopoverRef,
  creatingRootKind,
  creatingRootName,
  creatingRootBusy,
  setCreatingRootName,
  handleCreateRootSubmit,
  handleCreateRootCancel,
  worktreeBranches,
  worktreeBranchesLoading,
  worktreeBranchError,
  worktreeBranchMode,
  setWorktreeBranchMode,
  worktreeBranch,
  setWorktreeBranch,
  worktreeSwitchOpen,
  worktreeSwitchLoading,
  worktreeSwitchError,
  worktreeSwitchItems,
  switchingWorktreePath,
  currentRootId,
  findManagedRootByPath,
  handleSwitchWorktree,
}: Props) {
  const { t } = useI18n();
  const worktreeBranchSelector = creatingRootKind === "worktree" ? (
    <div style={{ display: "flex", flexDirection: "column", gap: "5px" }}>
      <Select
        value={worktreeBranchMode === "new" ? "__new__" : worktreeBranch}
        disabled={creatingRootBusy}
        onChange={(value) => {
          if (value === "__new__") {
            setWorktreeBranchMode("new");
            setWorktreeBranch("");
            return;
          }
          setWorktreeBranchMode("existing");
          setWorktreeBranch(value);
        }}
        options={[
          { value: "__new__", label: t("worktree.createBranch") },
          ...worktreeBranches.branches.map((branch) => ({
            value: branch.name,
            label: branch.current ? `${branch.name} ${t("worktree.current")}` : branch.name,
          })),
        ]}
      />
      {worktreeBranchesLoading ? (
        <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{t("worktree.loadingBranches")}</span>
      ) : worktreeBranchError ? (
        <span style={{ fontSize: "11px", color: "#b45309" }}>{worktreeBranchError}</span>
      ) : null}
    </div>
  ) : null;

  const worktreeCreateOverlay = creatingRootKind === "worktree" && creatingRootName !== null ? (
    <div
      ref={createPopoverRef}
      style={{
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
      }}
    >
      <div style={{ fontSize: "12px", fontWeight: 600, color: "var(--text-primary)" }}>worktree</div>
      <input
        value={creatingRootName}
        disabled={creatingRootBusy}
        autoFocus
        onChange={(event) => setCreatingRootName(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            void handleCreateRootSubmit();
          } else if (event.key === "Escape") {
            event.preventDefault();
            handleCreateRootCancel();
          }
        }}
        style={{
          width: "100%",
          borderRadius: "8px",
          border: "1px solid var(--border-color)",
          background: "transparent",
          color: "var(--text-primary)",
          fontSize: "12px",
          padding: "8px 10px",
          outline: "none",
          boxSizing: "border-box",
        }}
      />
      {worktreeBranchSelector}
      <div style={{ display: "flex" }}>
        <button
          type="button"
          disabled={creatingRootBusy || !String(creatingRootName || "").trim()}
          onClick={() => void handleCreateRootSubmit()}
          style={{
            width: "100%",
            border: "none",
            background: creatingRootBusy || !String(creatingRootName || "").trim()
              ? "color-mix(in srgb, var(--accent-color) 65%, transparent)"
              : "var(--accent-color)",
            color: "#fff",
            borderRadius: "8px",
            padding: "8px 10px",
            fontSize: "12px",
            fontWeight: 600,
            cursor: creatingRootBusy || !String(creatingRootName || "").trim()
              ? "not-allowed"
              : "pointer",
          }}
        >
          {creatingRootBusy ? t("worktree.processing") : t("worktree.create")}
        </button>
      </div>
    </div>
  ) : null;

  const worktreeSwitchOverlay = worktreeSwitchOpen ? (
    <div
      ref={switchPopoverRef}
      style={{
        width: "248px",
        maxWidth: "calc(100vw - 32px)",
        maxHeight: "360px",
        padding: "8px",
        borderRadius: "12px",
        border: "1px solid var(--border-color)",
        background: "var(--menu-bg)",
        boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
        display: "flex",
        flexDirection: "column",
        gap: "6px",
        overflow: "auto",
      }}
    >
      <div style={{ padding: "4px 6px 6px", fontSize: "12px", fontWeight: 600, color: "var(--text-primary)" }}>
        {t("worktree.switchTitle")}
      </div>
      {worktreeSwitchLoading ? (
        <div style={{ padding: "8px 6px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("common.loading")}</div>
      ) : worktreeSwitchError ? (
        <div style={{ padding: "8px 6px", fontSize: "12px", color: "#b45309" }}>{worktreeSwitchError}</div>
      ) : worktreeSwitchItems.length === 0 ? (
        <div style={{ padding: "8px 6px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("worktree.noSwitchable")}</div>
      ) : worktreeSwitchItems.map((item) => {
        const managed = findManagedRootByPath(item.path);
        const active = item.current || managed?.id === currentRootId;
        const busy = switchingWorktreePath === item.path;
        const name = String(item.path || "").replace(/[\\/]+$/, "").split(/[\\/]/).filter(Boolean).pop() || item.path;
        return (
          <button
            key={item.path}
            type="button"
            disabled={active || !!switchingWorktreePath}
            onClick={() => void handleSwitchWorktree(item)}
            style={{
              width: "100%",
              border: "none",
              background: active ? "var(--selection-bg)" : "transparent",
              color: active ? "var(--accent-color)" : "var(--text-primary)",
              borderRadius: "8px",
              padding: "8px 10px",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: "12px",
              textAlign: "left",
              cursor: active || switchingWorktreePath ? "default" : "pointer",
              opacity: switchingWorktreePath && !busy ? 0.56 : 1,
            }}
          >
            <span style={{ minWidth: 0, display: "flex", flexDirection: "column", gap: "2px" }}>
              <span style={{ fontSize: "12px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {name}
              </span>
              <span style={{ fontSize: "11px", color: "var(--text-secondary)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {item.branch || item.head?.slice(0, 8) || item.path}
              </span>
            </span>
            <span style={{ fontSize: "11px", color: active ? "var(--accent-color)" : "var(--text-secondary)", flexShrink: 0 }}>
              {busy ? "..." : active ? t("worktree.current") : managed ? t("worktree.switch") : t("worktree.join")}
            </span>
          </button>
        );
      })}
    </div>
  ) : null;

  return mode === "create" ? worktreeCreateOverlay : worktreeSwitchOverlay;
}

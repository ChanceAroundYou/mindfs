import React, { useEffect, useRef, useState } from "react";
import type { GitBranchItem } from "../../services/git";
import { useI18n } from "../../i18n/index";

export type WorktreeBranchSelectorProps = {
  branchMode: "new" | "existing";
  branch: string;
  branches: GitBranchItem[];
  disabled?: boolean;
  height?: number;
  maxWidth?: number;
  menuAlign?: "left" | "right";
  menuPlacement?: "top" | "bottom";
  onChange: (branchMode: "new" | "existing", branch: string) => void;
};

export function useWorktreeBranchSelector({
  branchMode,
  branch,
  branches,
  disabled = false,
  onChange,
}: WorktreeBranchSelectorProps) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const selectedBranch = branches.find((item) => item.name === branch);
  const label =
    branchMode === "new"
      ? (branch || t("worktree.createBranch"))
      : selectedBranch?.name || branch;

  useEffect(() => {
    if (disabled) {
      setOpen(false);
    }
  }, [disabled]);

  useEffect(() => {
    if (!open) return;

    const handlePointerOutside = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };

    document.addEventListener("pointerdown", handlePointerOutside);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerOutside);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  const selectBranch = (nextMode: "new" | "existing", nextBranch: string) => {
    onChange(nextMode, nextBranch);
    setOpen(false);
  };

  return {
    open,
    setOpen,
    containerRef,
    label,
    selectBranch,
  };
}

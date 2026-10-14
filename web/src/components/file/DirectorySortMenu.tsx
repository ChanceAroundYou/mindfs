import type { Dispatch, SetStateAction } from "react";
import { useI18n } from "../../i18n/index";
import { DIRECTORY_SORT_OPTIONS, type DirectorySortMode } from "../../shared/directorySort";

export type DirectorySortControlValue = DirectorySortMode | "inherit";

export type DirectorySortMenuProps = {
  isOpen: boolean;
  setIsOpen: Dispatch<SetStateAction<boolean>>;
  sortControlValue: DirectorySortControlValue;
  onSortModeChange?: (mode: DirectorySortControlValue) => void;
  sortLabel: (value: DirectorySortControlValue) => string;
  closeMenu: () => void;
};

export function DirectorySortMenu({
  isOpen,
  setIsOpen,
  sortControlValue,
  onSortModeChange,
  sortLabel,
  closeMenu,
}: DirectorySortMenuProps) {
  const { t } = useI18n();
  return (
    <>
      <button
        type="button"
        onClick={() => setIsOpen((open) => !open)}
        style={{
          width: "100%",
          border: "none",
          background: "transparent",
          color: "var(--text-primary)",
          borderRadius: "8px",
          padding: "8px 10px",
          display: "flex",
          alignItems: "center",
          gap: "8px",
          textAlign: "left",
          cursor: "pointer",
          fontSize: "12px",
        }}
        aria-expanded={isOpen}
      >
        <span style={{ flex: 1 }}>{t("directory.currentSort")}</span>
        <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
          {sortLabel(sortControlValue)}
        </span>
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{
            transform: isOpen ? "rotate(90deg)" : "rotate(0deg)",
            transition: "transform 0.15s cubic-bezier(0.4, 0, 0.2, 1)",
            color: isOpen ? "var(--text-primary)" : "#9ca3af",
          }}
        >
          <polyline points="9 18 15 12 9 6" />
        </svg>
      </button>
      {isOpen ? (
        <>
          <button
            type="button"
            onClick={() => {
              onSortModeChange?.("inherit");
              closeMenu();
              setIsOpen(false);
            }}
            style={{
              width: "100%",
              border: "none",
              background: sortControlValue === "inherit" ? "var(--selection-bg)" : "transparent",
              color: sortControlValue === "inherit" ? "var(--accent-color)" : "var(--text-primary)",
              borderRadius: "8px",
              padding: "8px 10px",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              textAlign: "left",
              cursor: "pointer",
              fontSize: "12px",
            }}
          >
            <span>{t("directory.followGlobal")}</span>
            <span style={{ fontSize: "11px", opacity: sortControlValue === "inherit" ? 1 : 0 }}>✓</span>
          </button>
          {DIRECTORY_SORT_OPTIONS.map((option) => {
            const active = sortControlValue === option.value;
            const value = option.value as DirectorySortControlValue;
            return (
              <button
                key={option.value}
                type="button"
                onClick={() => {
                  onSortModeChange?.(value);
                  closeMenu();
                  setIsOpen(false);
                }}
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
                  textAlign: "left",
                  cursor: "pointer",
                  fontSize: "12px",
                }}
              >
                <span>{sortLabel(value)}</span>
                <span style={{ fontSize: "11px", opacity: active ? 1 : 0 }}>✓</span>
              </button>
            );
          })}
        </>
      ) : null}
    </>
  );
}

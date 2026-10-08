import React from "react";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { useI18n } from "../../i18n/index";

// 路径导航组件
export function Breadcrumbs({
  root,
  rootDisplayName,
  path,
  onPathClick,
  editingRoot = false,
  rootDraft = "",
  rootRenaming = false,
  inputRef,
  onRootDraftChange,
  onRootRenameSubmit,
  onRootRenameCancel,
  rootColor = null,
}: {
  root?: string;
  rootDisplayName?: string;
  path: string;
  onPathClick?: (path: string) => void;
  editingRoot?: boolean;
  rootDraft?: string;
  rootRenaming?: boolean;
  inputRef?: React.RefObject<HTMLInputElement | null>;
  onRootDraftChange?: (value: string) => void;
  onRootRenameSubmit?: () => void;
  onRootRenameCancel?: () => void;
  rootColor?: string | null;
}) {
  const { t } = useI18n();
  const normalizedPath =
    root && path.startsWith(root)
      ? path.slice(root.length).replace(/^\/+/, "")
      : path;
  const parts = normalizedPath.split("/").filter(Boolean);

  const getPathAt = (index: number) => {
    return parts.slice(0, index + 1).join("/");
  };

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "4px",
        fontSize: "13px",
        color: "var(--text-secondary)",
        overflow: "hidden",
        whiteSpace: "nowrap",
        flex: 1,
        justifyContent: "flex-start",
      }}
    >
      {root && (
        <>
          {editingRoot ? (
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "4px",
                minWidth: 0,
                maxWidth: "min(320px, 60vw)",
              }}
            >
              <span
                style={{
                  display: "inline-block",
                  position: "relative",
                  minWidth: 0,
                  maxWidth: "100%",
                }}
              >
                <span
                  aria-hidden="true"
                  style={{
                    visibility: "hidden",
                    display: "block",
                    whiteSpace: "pre",
                    height: "24px",
                    border: "1px solid transparent",
                    fontSize: "13px",
                    fontWeight: 700,
                    padding: "0 10px",
                    boxSizing: "border-box",
                  }}
                >
                  {rootDraft || " "}
                </span>
                <input
                  ref={inputRef}
                  value={rootDraft}
                  disabled={rootRenaming}
                  onChange={(event) => {
                    const input = event.currentTarget;
                    onRootDraftChange?.(event.target.value);
                    window.requestAnimationFrame(() => {
                      if (input.scrollWidth <= input.clientWidth + 1) {
                        input.scrollLeft = 0;
                      }
                    });
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Escape") {
                      event.preventDefault();
                      onRootRenameCancel?.();
                      return;
                    }
                    if (event.key === "Enter") {
                      event.preventDefault();
                      onRootRenameSubmit?.();
                    }
                  }}
                  style={{
                    position: "absolute",
                    inset: 0,
                    minWidth: 0,
                    width: "100%",
                    maxWidth: "100%",
                    height: "24px",
                    borderRadius: "6px",
                    border: "1px solid var(--accent-color)",
                    background: "var(--content-bg, #fff)",
                    color: "var(--text-primary)",
                    fontSize: "13px",
                    fontWeight: 700,
                    padding: "0 8px",
                    outline: "none",
                    boxSizing: "border-box",
                  }}
                />
              </span>
              <button
                type="button"
                onMouseDown={(event) => event.preventDefault()}
                onClick={onRootRenameSubmit}
                disabled={rootRenaming}
                aria-label={t("sessionList.confirmRename")}
                style={{
                  width: "22px",
                  height: "22px",
                  borderRadius: "6px",
                  border: "none",
                  background: "transparent",
                  color: "var(--accent-color)",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  cursor: rootRenaming ? "default" : "pointer",
                  opacity: rootRenaming ? 0.6 : 1,
                  padding: 0,
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
                  aria-hidden="true"
                >
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              </button>
              <button
                type="button"
                onMouseDown={(event) => event.preventDefault()}
                onClick={onRootRenameCancel}
                disabled={rootRenaming}
                aria-label={t("directory.cancelRename")}
                style={{
                  width: "22px",
                  height: "22px",
                  borderRadius: "6px",
                  border: "none",
                  background: "transparent",
                  color: "var(--text-secondary)",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  cursor: rootRenaming ? "default" : "pointer",
                  opacity: rootRenaming ? 0.6 : 1,
                  padding: 0,
                  flexShrink: 0,
                }}
              >
                <svg
                  width="12"
                  height="12"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <line x1="18" y1="6" x2="6" y2="18" />
                  <line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            </span>
          ) : (
            <button
              type="button"
              data-onboarding="project-home"
              onClick={() => onPathClick?.(".")}
              style={{
                ...rootBadgeButtonStyle,
                background: "var(--node-badge-bg)",
                color: String(rootColor || "").trim() || "var(--text-primary)",
                cursor: "pointer",
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.textDecoration = "underline";
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.textDecoration = "none";
              }}
            >
              {rootDisplayName || root}
            </button>
          )}
          {parts.length > 0 && (
            <span style={{ opacity: 0.4, fontSize: "10px", flexShrink: 0 }}>
              ❯
            </span>
          )}
        </>
      )}
      {parts.map((part, index) => (
        <React.Fragment key={index}>
          <span
            onClick={() => onPathClick?.(getPathAt(index))}
            style={{
              fontWeight: index === parts.length - 1 ? 600 : 400,
              color:
                index === parts.length - 1 ? "var(--text-primary)" : "inherit",
              cursor: "pointer",
              flexShrink: index === parts.length - 1 ? 0 : 1,
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.textDecoration = "underline";
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.textDecoration = "none";
            }}
          >
            {part}
          </span>
          {index < parts.length - 1 && (
            <span style={{ opacity: 0.4, fontSize: "10px", flexShrink: 0 }}>
              ❯
            </span>
          )}
        </React.Fragment>
      ))}
    </div>
  );
}

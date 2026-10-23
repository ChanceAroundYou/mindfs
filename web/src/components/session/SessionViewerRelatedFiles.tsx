import React from "react";

export function SessionViewerRelatedFiles({ relatedFiles, relatedFileStatsByKey, gitFileStatsByPath, relatedFilesCollapsed, setRelatedFilesCollapsed, relatedFilesDividerRef, displayFileGroups, rootPath, onFileClickRef, onRemoveRelatedFile, interactionMode, t, relatedFileStatKey }: any) {
  return (
    <>
      {relatedFiles.length > 0 && interactionMode !== "drawer" && (
  <div
    ref={relatedFilesDividerRef}
    style={{
      marginTop: "8px",
      paddingTop: "8px",
      borderTop: "1px solid var(--border-color)",
      width: "100%",
      boxSizing: "border-box",
    }}
  >
    <div
      role="button"
      tabIndex={0}
      onClick={() => setRelatedFilesCollapsed((value: boolean) => !value)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          setRelatedFilesCollapsed((value: boolean) => !value);
        }
      }}
      style={{
        fontSize: "12px",
        fontWeight: 500,
        color: "var(--text-secondary)",
        marginBottom: "4px",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        cursor: "pointer",
        borderRadius: "6px",
        padding: "2px 0",
        outline: "none",
      }}
    >
      <span>{t("session.relatedFiles", { count: relatedFiles.length })}</span>
      <div
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: "10px",
        }}
      >
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation();
            setRelatedFilesCollapsed((value: boolean) => !value)
          }}
          aria-label={
            relatedFilesCollapsed ? t("session.expandRelatedFiles") : t("session.collapseRelatedFiles")
          }
          title={
            relatedFilesCollapsed ? t("session.expandRelatedFiles") : t("session.collapseRelatedFiles")
          }
          style={{
            border: "none",
            background: "transparent",
            padding: 0,
            margin: 0,
            cursor: "pointer",
            color: "var(--text-secondary)",
            width: "16px",
            height: "16px",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            flexShrink: 0,
          }}
        >
          <span
            aria-hidden="true"
            style={{
              flexShrink: 0,
              transform: relatedFilesCollapsed
                ? "rotate(0deg)"
                : "rotate(90deg)",
              transition: "transform 0.2s",
              color: "var(--text-secondary)",
              display: "inline-flex",
              alignItems: "center",
            }}
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.25"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <polyline points="9 18 15 12 9 6" />
            </svg>
          </span>
        </button>
      </div>
    </div>
    {!relatedFilesCollapsed ? (
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "4px",
        }}
      >
        {displayFileGroups.map((group: any) => {
          const normalizedRepoPath = String(group.repoPath || "").replace(/[\\/]+$/, "");
          const normalizedRootPath = String(rootPath || "").replace(/[\\/]+$/, "");
          const isCurrentRepo =
            !group.repoPath ||
            group.repoName === t("session.currentProject") ||
            normalizedRepoPath === normalizedRootPath;
          const showGroupHeader = group.repoKind === "plain" || group.head || !isCurrentRepo;
          return (
            <div
              key={group.key}
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "4px",
              }}
            >
              {showGroupHeader ? (
                <div
                  title={[group.repoPath, group.head].filter(Boolean).join(" · ") || group.repoName || t("session.currentProject")}
                  style={{
                    padding: "2px 6px 0",
                    fontSize: "11px",
                    color: "var(--text-secondary)",
                    fontFamily: group.head
                      ? "var(--mono-font, monospace)"
                      : undefined,
                  }}
                >
                  {group.repoKind === "plain"
                    ? `${group.repoName || t("session.currentProject")} · ${t("session.nonGit")}`
                    : group.head
                      ? isCurrentRepo
                        ? `HEAD ${group.head.slice(0, 8)}`
                        : `${group.repoName || t("session.currentProject")} · HEAD ${group.head.slice(0, 8)}`
                      : group.repoName || t("session.currentProject")}
                </div>
              ) : null}
              {group.files.map((file: any) => {
                const stats =
                  relatedFileStatsByKey[relatedFileStatKey(file)] ||
                  gitFileStatsByPath[file.path];
                return (
              <div
                key={`${file.head || "legacy"}:${file.path}`}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "6px",
                }}
              >
                <div
                  onClick={() => onFileClickRef.current?.(file)}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    flex: 1,
                    minWidth: 0,
                    padding: "3px 6px",
                    borderRadius: "6px",
                    cursor: "pointer",
                    transition: "background 0.15s",
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background =
                      "rgba(0,0,0,0.04)";
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background =
                      "transparent";
                  }}
                >
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="13"
                    height="13"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="#94a3b8"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden="true"
                    style={{ flexShrink: 0 }}
                  >
                    <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                    <polyline points="14 2 14 8 20 8" />
                    <line x1="16" x2="8" y1="13" y2="13" />
                    <line x1="16" x2="8" y1="17" y2="17" />
                    <line x1="10" x2="8" y1="9" y2="9" />
                  </svg>
                  <div
                    style={{
                      flex: 1,
                      minWidth: 0,
                      fontSize: "12px",
                      color: "var(--text-primary)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {file.name}
                  </div>
                  {stats ? (
                    <div
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: "8px",
                        fontSize: "11px",
                        color: "var(--text-secondary)",
                        flexShrink: 0,
                      }}
                    >
                      <span
                        style={{
                          color: "#15803d",
                          fontVariantNumeric: "tabular-nums",
                        }}
                      >
                        +{stats.additions}
                      </span>
                      <span
                        style={{
                          color: "#b91c1c",
                          fontVariantNumeric: "tabular-nums",
                        }}
                      >
                        -{stats.deletions}
                      </span>
                    </div>
                  ) : null}
                </div>
                <button
                  type="button"
                  aria-label={t("session.removeRelatedFile", { name: file.name || file.path })}
                  onClick={(event) => {
                    event.stopPropagation();
                    onRemoveRelatedFile?.(
                      file.path,
                      file.head,
                      file.repo_path,
                      file.repo_kind,
                    );
                  }}
                  style={{
                    border: "none",
                    background: "transparent",
                    color: "#dc2626",
                    cursor: "pointer",
                    fontSize: "14px",
                    lineHeight: 1,
                    padding: "2px 4px",
                    borderRadius: "4px",
                    flexShrink: 0,
                  }}
                >
                  x
                </button>
              </div>
                );
              })}
            </div>
          );
        })}
      </div>
    ) : null}
      </div>
      )}
    </>
  );
}

import React, { useState } from "react";
import { savePrompt, deletePrompt } from "../../services/prompts";
import { useI18n } from "../../i18n";
import { type CandidateItem } from "../../services/candidates";

function candidateNameColor(candidateType: CandidateItem["type"], isDark: boolean): string {
  switch (candidateType) {
    case "slash_command":
      return isDark ? "#93c5fd" : "#1d4ed8";
    case "prompt":
      return isDark ? "#fcd34d" : "#b45309";
    case "skill":
      return isDark ? "#c4b5fd" : "#7c3aed";
    default:
      return "var(--text-primary)";
  }
}

export function CandidateDropdown({
  candidates,
  activeCandidateIndex,
  candidateItemRefs,
  isDark,
  isMobile,
  activeTokenType,
  mode,
  accentColorRaw,
  accentHex,
  applyCandidate,
  onPromptSaved,
  onPromptDeleted,
  onPromptFormClosed,
}: {
  candidates: CandidateItem[];
  activeCandidateIndex: number;
  candidateItemRefs: React.MutableRefObject<Array<HTMLDivElement | null>>;
  isDark: boolean;
  isMobile: boolean;
  activeTokenType: "file" | "slash" | "prompt" | "command";
  mode: string;
  accentColorRaw: string;
  accentHex: string;
  applyCandidate: (candidate: CandidateItem) => void;
  onPromptSaved: () => void;
  onPromptDeleted: (text: string) => void;
  /** prompt 表单关闭（取消或保存成功）后回调，父层用它把焦点还给编辑器。 */
  onPromptFormClosed?: () => void;
}) {
  const { t } = useI18n();
  const [addingPrompt, setAddingPrompt] = useState(false);
  const [newPromptText, setNewPromptText] = useState("");
  const [savingPrompt, setSavingPrompt] = useState(false);
  const [deletingPrompt, setDeletingPrompt] = useState("");
  const [promptSaveError, setPromptSaveError] = useState("");

  const cancelAddPrompt = () => {
    if (savingPrompt) return;
    setAddingPrompt(false);
    setNewPromptText("");
    setPromptSaveError("");
    onPromptFormClosed?.();
  };

  const submitNewPrompt = async () => {
    const text = newPromptText.trim();
    if (!text || savingPrompt) return;
    setSavingPrompt(true);
    setPromptSaveError("");
    try {
      await savePrompt(text);
      setAddingPrompt(false);
      setNewPromptText("");
      onPromptSaved();
      onPromptFormClosed?.();
    } catch (error) {
      setPromptSaveError(String((error as Error)?.message || t("action.addPromptFailed")));
    } finally {
      setSavingPrompt(false);
    }
  };

  const removePrompt = async (text: string) => {
    if (deletingPrompt) return;
    setDeletingPrompt(text);
    setPromptSaveError("");
    try {
      await deletePrompt(text);
      onPromptDeleted(text);
    } catch (error) {
      setPromptSaveError(String((error as Error)?.message || t("action.deletePromptFailed")));
    } finally {
      setDeletingPrompt("");
    }
  };

  return (
    <div
      style={{
        position: "absolute",
        left: "8px",
        right: "8px",
        bottom: "calc(100% + 8px)",
        background: "var(--menu-bg)",
        border: "1px solid var(--menu-border)",
        borderRadius: "12px",
        boxShadow: "0 12px 32px rgba(0,0,0,0.16)",
        overflowX: "hidden",
        overflowY: "auto",
        maxHeight: isMobile ? "min(55vh, 416px)" : "320px",
        WebkitOverflowScrolling: "touch",
        scrollbarWidth: "thin",
        zIndex: 20,
      }}
    >
      {candidates.length === 0 ? (
        <div
          style={{
            padding: "11px 12px",
            fontSize: "12px",
            color: "var(--text-secondary)",
            lineHeight: 1.5,
          }}
        >
          {activeTokenType === "command" ? t("action.noCommandHistory") : t("action.noPromptFavorites")}
        </div>
      ) : (
        candidates.map((candidate, index) => (
          <div
            key={`${candidate.type}:${candidate.name}`}
            ref={(element) => {
              candidateItemRefs.current[index] = element;
            }}
            onMouseDown={(e) => {
              e.preventDefault();
              applyCandidate(candidate);
            }}
            role="option"
            aria-selected={index === activeCandidateIndex}
            style={{
              display: "flex",
              flexDirection: candidate.type === "command" ? "row" : "column",
              alignItems: candidate.type === "command" ? "center" : "flex-start",
              gap: candidate.type === "command" ? "0" : "2px",
              width: "100%",
              padding: candidate.type === "command" ? "8px 12px" : "10px 12px",
              border: "none",
              borderTop: index === 0 ? "none" : "1px solid var(--menu-divider)",
              background: index === activeCandidateIndex ? "var(--menu-active-bg)" : "transparent",
              color: "var(--text-primary)",
              cursor: "pointer",
              textAlign: "left",
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: "8px", width: "100%", minWidth: 0 }}>
              <span style={{
                fontSize: "13px",
                fontWeight: 500,
                color: candidateNameColor(candidate.type, isDark),
                minWidth: 0,
                flex: 1,
                overflow: candidate.type === "command" || candidate.type === "prompt" ? "hidden" : "visible",
                textOverflow: candidate.type === "command" || candidate.type === "prompt" ? "ellipsis" : "clip",
                whiteSpace: candidate.type === "command" || candidate.type === "prompt" ? "nowrap" : "normal",
              }}>
                {candidate.type === "file" ? (mode === "command" ? candidate.name : `@${candidate.name}`) : candidate.type === "prompt" ? `#${candidate.name}` : candidate.type === "command" ? candidate.name : `/${candidate.name}`}
              </span>
              {candidate.type === "prompt" ? (
                <button
                  type="button"
                  onMouseDown={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                  }}
                  onClick={(event) => {
                    event.stopPropagation();
                    void removePrompt(candidate.name);
                  }}
                  disabled={!!deletingPrompt}
                  aria-label={t("action.deletePrompt", { name: candidate.name })}
                  title={t("common.delete")}
                  style={{ width: "28px", height: "28px", margin: "-5px -4px -5px 0", flexShrink: 0, border: "none", borderRadius: "7px", background: "transparent", color: "#dc2626", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: deletingPrompt ? "wait" : "pointer", opacity: deletingPrompt && deletingPrompt !== candidate.name ? 0.35 : 1 }}
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <polyline points="3 6 5 6 21 6" />
                    <path d="M19 6l-1 14H6L5 6" />
                    <path d="M10 11v6" />
                    <path d="M14 11v6" />
                    <path d="M9 6V4h6v2" />
                  </svg>
                </button>
              ) : null}
            </div>
            {candidate.type !== "command" && candidate.description ? (
              <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{candidate.description}</span>
            ) : null}
          </div>
        ))
      )}
      {activeTokenType === "prompt" ? (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "5px",
            padding: addingPrompt ? "8px" : 0,
            borderTop: "1px solid var(--menu-divider)",
            background: "var(--menu-bg)",
          }}
        >
          {addingPrompt ? (
            <>
              <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
                <input
                  value={newPromptText}
                  onChange={(event) => {
                    setNewPromptText(event.currentTarget.value);
                    if (promptSaveError) setPromptSaveError("");
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !event.nativeEvent.isComposing) {
                      event.preventDefault();
                      void submitNewPrompt();
                    } else if (event.key === "Escape") {
                      event.preventDefault();
                      cancelAddPrompt();
                    }
                  }}
                  placeholder={t("action.addPromptPlaceholder")}
                  aria-label={t("action.addPromptPlaceholder")}
                  disabled={savingPrompt}
                  autoFocus
                  style={{
                    minWidth: 0,
                    flex: 1,
                    height: "32px",
                    padding: "0 9px",
                    border: "1px solid var(--accent-color)",
                    borderRadius: "8px",
                    outline: "none",
                    background: "var(--panel-bg)",
                    color: "var(--text-primary)",
                    fontSize: "13px",
                  }}
                />
                <button
                  type="button"
                  onClick={() => void submitNewPrompt()}
                  disabled={!newPromptText.trim() || savingPrompt}
                  aria-label={t("common.save")}
                  title={t("common.save")}
                  style={{ width: "32px", height: "32px", border: "none", borderRadius: "8px", background: "transparent", color: newPromptText.trim() && !savingPrompt ? "var(--accent-color)" : "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: newPromptText.trim() && !savingPrompt ? "pointer" : "not-allowed", opacity: newPromptText.trim() && !savingPrompt ? 1 : 0.45 }}
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M20 6 9 17l-5-5" />
                  </svg>
                </button>
                <button
                  type="button"
                  onClick={cancelAddPrompt}
                  disabled={savingPrompt}
                  aria-label={t("common.cancel")}
                  title={t("common.cancel")}
                  style={{ width: "32px", height: "32px", border: "none", borderRadius: "8px", background: "transparent", color: "var(--text-secondary)", display: "inline-flex", alignItems: "center", justifyContent: "center", cursor: savingPrompt ? "not-allowed" : "pointer" }}
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M18 6 6 18" />
                    <path d="m6 6 12 12" />
                  </svg>
                </button>
              </div>
              {promptSaveError ? (
                <div role="alert" style={{ padding: "0 2px", color: "#dc2626", fontSize: "11px", lineHeight: 1.4 }}>
                  {promptSaveError}
                </div>
              ) : null}
            </>
          ) : (
            <>
              {promptSaveError ? (
                <div role="alert" style={{ padding: "7px 12px 0", color: "#dc2626", fontSize: "11px", lineHeight: 1.4 }}>
                  {promptSaveError}
                </div>
              ) : null}
              <button
                type="button"
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => {
                  setPromptSaveError("");
                  setAddingPrompt(true);
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "7px",
                  width: "100%",
                  padding: "9px 12px",
                  border: "none",
                  background: "transparent",
                  color: "var(--accent-color)",
                  fontSize: "13px",
                  fontWeight: 600,
                  textAlign: "left",
                  cursor: "pointer",
                }}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
                  <path d="M12 5v14" />
                  <path d="M5 12h14" />
                </svg>
                {t("action.addPrompt")}
              </button>
            </>
          )}
        </div>
      ) : null}
    </div>
  );
}

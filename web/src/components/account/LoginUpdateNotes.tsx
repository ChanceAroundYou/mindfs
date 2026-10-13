import { useI18n } from "../../i18n/index";

export type LoginUpdateNotesProps = {
  notes: string;
  open: boolean;
  onToggle: () => void;
};

export function LoginUpdateNotes({ notes, open, onToggle }: LoginUpdateNotesProps) {
  const { t } = useI18n();
  if (!notes) return null;

  return (
    <>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "8px",
          flexWrap: "wrap",
        }}
      >
        <button
          type="button"
          onClick={onToggle}
          style={{
            border: "none",
            background: "transparent",
            color: "var(--mindfs-launcher-accent)",
            padding: 0,
            justifySelf: "start",
            fontSize: "12px",
            fontWeight: 600,
            cursor: "pointer",
          }}
        >
          {open ? t("login.hideUpdateNotes") : t("login.showUpdateNotes")}
        </button>
        <span
          style={{
            color: "#dc2626",
            fontSize: "12px",
            fontWeight: 700,
            lineHeight: 1.35,
          }}
        >
          {t("login.backendUpgradeRequired")}
        </span>
      </div>
      {open ? (
        <div
          style={{
            borderTop: "1px solid var(--mindfs-launcher-border)",
            paddingTop: "10px",
            color: "var(--mindfs-launcher-muted)",
            fontSize: "12px",
            lineHeight: 1.55,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            maxHeight: "180px",
            overflow: "auto",
          }}
        >
          {notes}
        </div>
      ) : null}
    </>
  );
}

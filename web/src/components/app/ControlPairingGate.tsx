// 边控端首次配对的表单：只在「等着配对」的边缘上出现。
//
// 它存在的理由很具体：这台机器以 edge 角色起来了，但 token 还没落地——
// 不配对就只是个孤立的本地控制面。而拿 token 的唯一入口就是这一页。
//
// 视图层只管渲染与本地校验；提交、状态、失败分类都在 useControlSyncStatus，
// 网络细节留在 service 层（仓库架构红线：components 不 import services）。
// 所有数据都从 props 进来 —— 由 ControlSyncProvider 那唯一一处轮询持有，两处各自
// 轮询会让「表单说没配上、横幅说在同步」这种事真的发生。
//
// 挂到 document.body：主区有 contain: paint，fixed 浮层留在 App 树里会被侧栏顶偏。
import React, { useState } from "react";
import { createPortal } from "react-dom";
import { PanelShell } from "../shell/PanelShell";
import { useI18n } from "../../i18n";
import { pairingDraftIssue, type PairingDraft } from "../../shared/controlPairing";
import type {
  ControlSyncPairResult,
  ControlSyncPairingState,
  PairControlSyncOutcome,
} from "../../hooks/useControlSyncStatus";

const FIELD_STYLE: React.CSSProperties = {
  width: "100%",
  padding: "6px 9px",
  borderRadius: 8,
  border: "1px solid var(--border-color)",
  background: "var(--panel-bg)",
  color: "var(--text-primary, inherit)",
  font: "inherit",
  fontSize: 13,
  boxSizing: "border-box",
};

const HINT_STYLE: React.CSSProperties = {
  fontSize: 11,
  lineHeight: 1.45,
  color: "var(--text-secondary)",
};

const BUTTON_STYLE: React.CSSProperties = {
  padding: "4px 12px",
  borderRadius: 7,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary, inherit)",
  font: "inherit",
  fontSize: 12,
  cursor: "pointer",
};

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <span style={{ fontSize: 12, color: "var(--text-secondary)" }}>{label}</span>
      {children}
      {hint ? <span style={HINT_STYLE}>{hint}</span> : null}
    </label>
  );
}

export type ControlPairingGateProps = {
  pairing: ControlSyncPairingState | null;
  open: boolean;
  busy: boolean;
  failure: PairControlSyncOutcome | null;
  paired: ControlSyncPairResult | null;
  onOpen: () => void;
  onClose: () => void;
  onSubmit: (draft: PairingDraft) => void;
};

function PairingForm({
  busy,
  failure,
  paired,
  instanceID,
  onClose,
  onSubmit,
}: {
  busy: boolean;
  failure: PairControlSyncOutcome | null;
  paired: ControlSyncPairResult | null;
  instanceID?: string;
  onClose: () => void;
  onSubmit: (draft: PairingDraft) => void;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<PairingDraft>({ primaryURL: "", token: "", caFile: "" });
  // 只在点提交时算：一开始就红着说「地址不合法」等于骂用户还没填的输入框。
  const [localIssue, setLocalIssue] = useState<ReturnType<typeof pairingDraftIssue>>(null);

  const failureText = (() => {
    if (!failure || failure.ok) return "";
    switch (failure.kind) {
      case "not-local":
        return t("controlPairing.errorNotLocal");
      case "unsupported":
        return t("controlPairing.errorUnsupported");
      case "unreachable":
        return t("controlPairing.errorUnreachable");
      default:
        return t("controlPairing.errorRejected", { message: failure.message });
    }
  })();

  const issueText = (() => {
    if (!localIssue) return "";
    if (localIssue === "url") return t("controlPairing.errorUrl");
    if (localIssue === "urlScheme") return t("controlPairing.errorScheme");
    return t("controlPairing.errorToken");
  })();

  return (
    <PanelShell title={t("controlPairing.title")} width={560} onClose={onClose} showCloseButton>
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <span style={HINT_STYLE}>{t("controlPairing.hint")}</span>
        <span style={{ ...HINT_STYLE, color: "var(--status-warn, #d08700)" }}>
          {t("controlPairing.localOnly")}
        </span>
        {paired ? (
          <div
            role="status"
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 6,
              padding: "8px 10px",
              borderRadius: 8,
              border: "1px solid color-mix(in srgb, var(--status-good, #2e7d32) 45%, transparent)",
              background: "color-mix(in srgb, var(--status-good, #2e7d32) 10%, transparent)",
              fontSize: 12,
            }}
          >
            {/* 表单不消失：用户刚点的那一下必须有落点，否则会以为没点中。 */}
            <span>
              {t("controlPairing.done", {
                url: paired.primary_url ?? "",
                version: paired.primary_version ?? "",
              })}
            </span>
            {paired.restart_required ? (
              <span style={{ color: "var(--text-secondary)" }}>
                {t("controlPairing.restartRequired")}
              </span>
            ) : null}
          </div>
        ) : (
          <>
            <Field label={t("controlPairing.primaryURL")}>
              <input
                value={draft.primaryURL}
                placeholder={t("controlPairing.primaryURLPlaceholder")}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setDraft((d) => ({ ...d, primaryURL: e.target.value }))}
                style={FIELD_STYLE}
              />
            </Field>
            <Field label={t("controlPairing.token")} hint={t("controlPairing.tokenHint")}>
              <input
                value={draft.token}
                placeholder={t("controlPairing.tokenPlaceholder")}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setDraft((d) => ({ ...d, token: e.target.value }))}
                style={FIELD_STYLE}
              />
            </Field>
            <Field label={t("controlPairing.caFile")} hint={t("controlPairing.caFileHint")}>
              <input
                value={draft.caFile}
                placeholder={t("controlPairing.caFilePlaceholder")}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setDraft((d) => ({ ...d, caFile: e.target.value }))}
                style={FIELD_STYLE}
              />
            </Field>
            {issueText ? (
              <span role="alert" style={{ ...HINT_STYLE, color: "var(--status-bad, #c62828)" }}>
                {issueText}
              </span>
            ) : null}
            {failureText ? (
              <span role="alert" style={{ ...HINT_STYLE, color: "var(--status-bad, #c62828)" }}>
                {failureText}
              </span>
            ) : null}
            <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
              <button type="button" onClick={onClose} style={BUTTON_STYLE}>
                {t("controlPairing.close")}
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => {
                  // 本地先判一次：让用户在出错的输入框旁看到原因，而不是等一个 400。
                  const issue = pairingDraftIssue(draft);
                  setLocalIssue(issue);
                  if (issue) return;
                  onSubmit(draft);
                }}
                style={{ ...BUTTON_STYLE, opacity: busy ? 0.5 : 1 }}
              >
                {busy ? t("controlPairing.submitting") : t("controlPairing.submit")}
              </button>
            </div>
          </>
        )}
        {instanceID ? <span style={HINT_STYLE}>instance_id: {instanceID}</span> : null}
      </div>
    </PanelShell>
  );
}

/**
 * 未配对的边缘：一条常驻提示 + 打开表单。已配对或不是边缘时什么都不渲染。
 */
export function ControlPairingGate(props: ControlPairingGateProps) {
  const { pairing, open, busy, failure, paired, onOpen, onClose, onSubmit } = props;
  if (!pairing?.pending_pairing) return null;
  if (open) {
    return createPortal(
      <PairingForm
        busy={busy}
        failure={failure}
        paired={paired}
        instanceID={pairing.instance_id}
        onClose={onClose}
        onSubmit={onSubmit}
      />,
      document.body,
    );
  }
  return createPortal(<PendingBanner onOpen={onOpen} />, document.body);
}

function PendingBanner({ onOpen }: { onOpen: () => void }) {
  const { t } = useI18n();
  return (
    <div
      role="status"
      style={{
        position: "fixed",
        top: 8,
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: 899,
        display: "flex",
        alignItems: "center",
        gap: 10,
        maxWidth: "min(92vw, 760px)",
        padding: "8px 12px",
        borderRadius: 10,
        // 与在线横幅同一套弱化口径：它是一条常驻状态，不该抢冲突横幅的注意力。
        border: "1px solid color-mix(in srgb, var(--status-warn, #d08700) 25%, transparent)",
        background: "color-mix(in srgb, var(--status-warn, #d08700) 6%, var(--panel-bg))",
        opacity: 0.85,
        fontSize: 13,
      }}
    >
      <span>{t("controlPairing.pendingTitle")}</span>
      <span style={{ color: "var(--text-secondary)" }}>{t("controlPairing.pendingHint")}</span>
      <button type="button" onClick={onOpen} style={BUTTON_STYLE}>
        {t("controlPairing.open")}
      </button>
    </div>
  );
}

import React from "react";
import { useI18n } from "../../i18n/index";
import {
  getWebPushStatus,
  sendWebPushTest,
  subscribeWebPush,
  unsubscribeWebPush,
  webPushReasonLabel,
  type WebPushStatus,
} from "../../services/webPush";
import { SymlinkBadge } from "./SymlinkBadge";
import type { FileEntry } from "../../services/prefs/directorySort";

export const fileTreeMenuButtonStyle: React.CSSProperties = {
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
};

export function NotificationIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 7h18s-3 0-3-7" />
      <path d="M13.73 21a2 2 0 0 1-3.46 0" />
    </svg>
  );
}

export function InfoIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 48 48" aria-hidden="true">
      <path d="M0 0h48v48H0z" fill="none" />
      <g fill="none">
        <path stroke="currentColor" strokeLinejoin="round" strokeWidth="4" d="M24 44a19.94 19.94 0 0 0 14.142-5.858A19.94 19.94 0 0 0 44 24a19.94 19.94 0 0 0-5.858-14.142A19.94 19.94 0 0 0 24 4A19.94 19.94 0 0 0 9.858 9.858A19.94 19.94 0 0 0 4 24a19.94 19.94 0 0 0 5.858 14.142A19.94 19.94 0 0 0 24 44Z" />
        <path fill="currentColor" fillRule="evenodd" d="M24 11a2.5 2.5 0 1 1 0 5a2.5 2.5 0 0 1 0-5" clipRule="evenodd" />
        <path stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="4" d="M24.5 34V20h-2M21 34h7" />
      </g>
    </svg>
  );
}

export function WebPushMenuItem() {
  const { t } = useI18n();
  const [status, setStatus] = React.useState<WebPushStatus | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [message, setMessage] = React.useState("");
  const [expanded, setExpanded] = React.useState(false);

  const refresh = React.useCallback(async () => {
    try {
      setStatus(await getWebPushStatus());
    } catch (error) {
      setStatus(null);
      setMessage(error instanceof Error ? error.message : t("fileTree.notificationStatusFailed"));
    }
  }, []);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  const run = async (action: "subscribe" | "unsubscribe" | "test") => {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      if (action === "subscribe") {
        setStatus(await subscribeWebPush());
      } else if (action === "unsubscribe") {
        setStatus(await unsubscribeWebPush());
      } else {
        await sendWebPushTest();
        setMessage(t("fileTree.notificationSent"));
        await refresh();
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : t("fileTree.notificationActionFailed"));
      await refresh();
    } finally {
      setBusy(false);
    }
  };

  const disabledReason = webPushReasonLabel(status?.reason);
  const enabled = Boolean(status?.enabled && status.supported);
  const subscribed = Boolean(status?.subscribed);
  const label = subscribed ? t("fileTree.notificationEnabled") : t("fileTree.enableNotification");
  const subscriptionCount = status?.subscription_count || 0;
  const currentDeviceDetail = subscribed && subscriptionCount > 0
    ? t("fileTree.notificationSubscribedDevices", { count: subscriptionCount })
    : subscribed
      ? t("fileTree.notificationDetailSubscribed")
      : t("fileTree.notificationDetailIOS");
  const detail = message || disabledReason || currentDeviceDetail;

  return (
    <div>
      <div
        style={{
          ...fileTreeMenuButtonStyle,
          color: subscribed ? "var(--accent-color)" : "var(--text-primary)",
          opacity: busy || !enabled ? 0.55 : 1,
          cursor: "default",
          minWidth: 0,
          whiteSpace: "nowrap",
        }}
      >
        <button
          type="button"
          disabled={busy || !enabled}
          onClick={() => void run(subscribed ? "unsubscribe" : "subscribe")}
          style={{
            minWidth: 0,
            border: "none",
            background: "transparent",
            color: "inherit",
            padding: 0,
            display: "inline-flex",
            alignItems: "center",
            gap: "8px",
            flex: "0 1 auto",
            cursor: busy || !enabled ? "not-allowed" : "pointer",
            font: "inherit",
            textAlign: "left",
            whiteSpace: "nowrap",
          }}
        >
          <NotificationIcon />
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{busy ? t("fileTree.notificationBusy") : label}</span>
        </button>
        <button
          type="button"
          aria-label={t("fileTree.notificationInfo")}
          title={t("fileTree.notificationInfo")}
          onClick={() => setExpanded((value) => !value)}
          style={{
            border: "none",
            background: "transparent",
            color: expanded ? "var(--accent-color)" : "var(--text-secondary)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            cursor: "pointer",
            flexShrink: 0,
            padding: 0,
            marginLeft: "-4px",
          }}
        >
          <InfoIcon />
        </button>
        <span style={{ marginLeft: "auto", fontSize: "11px", opacity: subscribed ? 1 : 0, flexShrink: 0 }}>✓</span>
      </div>
      {expanded ? (
        <>
          <div style={{ padding: "0 10px 6px 32px", color: "var(--text-secondary)", fontSize: "11px", lineHeight: 1.35 }}>
            {detail}
          </div>
          {subscribed ? (
            <button
              type="button"
              disabled={busy}
              onClick={() => void run("test")}
              style={{
                ...fileTreeMenuButtonStyle,
                paddingLeft: "32px",
                color: "var(--text-secondary)",
                opacity: busy ? 0.55 : 1,
                cursor: busy ? "not-allowed" : "pointer",
              }}
            >
              <span>{t("fileTree.sendTestNotification")}</span>
            </button>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

export const ChevronRight = ({ isOpen }: { isOpen: boolean }) => (
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
);

export function RestartSpinner() {
  return (
    <span
      aria-label="restarting"
      style={{
        width: "12px",
        height: "12px",
        border: "1.5px solid currentColor",
        borderTopColor: "transparent",
        borderRadius: "50%",
        animation: "mindfs-update-spin 0.8s linear infinite",
        display: "inline-block",
        boxSizing: "border-box",
      }}
    />
  );
}

export function DirectoryIconSlot({ entry, isOpen }: { entry: FileEntry; isOpen: boolean }) {
  const showSymlinkBadge = entry.is_dir && entry.is_symlink;

  return (
    <div style={{ position: "relative", width: 20, height: 18, display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0 }}>
      {entry.is_dir ? <ChevronRight isOpen={isOpen} /> : getFileIcon(entry.name)}
      {showSymlinkBadge ? (
        <SymlinkBadge offset="-1px" />
      ) : null}
    </div>
  );
}

export const getFileIcon = (filename: string) => {
  const ext = filename.split('.').pop()?.toLowerCase();

  // 核心文件类型使用极简 SVG
  if (['js', 'ts', 'jsx', 'tsx', 'go', 'py', 'java', 'c', 'cpp'].includes(ext!)) {
    return (
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.8 }}>
        <path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"/>
        <polyline points="14 2 14 8 20 8"/>
      </svg>
    );
  }
  if (['md', 'txt'].includes(ext!)) {
    return (
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.6 }}>
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/>
      </svg>
    );
  }
  if (['png', 'jpg', 'jpeg', 'gif', 'svg'].includes(ext!)) {
    return (
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.7 }}>
        <rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>
      </svg>
    );
  }

  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.5 }}>
      <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="13 2 13 9 20 9"/>
    </svg>
  );
};

export function ConfigArchiveIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 512 512" aria-hidden="true">
      <path d="M0 0h512v512H0z" fill="none" />
      <path fill="currentColor" fillRule="evenodd" d="M352 168.296c64.802 0 117.334 29.715 117.334 66.37q0 1.34-.093 2.665l.028-.294h.065v165.925c0 36.656-52.532 66.37-117.334 66.37c-63.361 0-114.992-28.408-117.256-63.936l-.077-2.434V237.037h.073a38 38 0 0 1-.073-2.37c0-36.656 52.532-66.371 117.333-66.371m0 218.074c-28.365 0-54.38-5.694-74.667-15.171v22.317l.018 1.196c.684 12.202 32.466 31.954 74.657 31.954c23.075 0 44.362-5.789 59.26-15.367c10.256-6.594 14.782-12.873 15.34-16.01l.059-.623V371.2c-20.286 9.477-46.3 15.17-74.667 15.17m0-85.333c-28.361 0-54.373-5.693-74.658-15.167l-.002 35.906l1.446-.01c1.73 1.73 5.179 4.59 11.254 8.027c15.143 8.566 37.48 13.91 61.96 13.91s46.818-5.344 61.96-13.91c7.501-4.242 11-7.608 12.2-9.05l.507-.003l.003-34.875c-20.287 9.477-46.303 15.172-74.67 15.172m0-90.075c-41.237 0-74.666 10.984-74.666 24.534s33.43 24.533 74.666 24.533c41.238 0 74.667-10.984 74.667-24.533s-33.43-24.534-74.667-24.534M101.72 51.61l30.173 30.173C109.67 104.807 96 136.14 96 170.666c0 42.82 21.026 80.728 53.316 103.965l.018-82.632H192v149.334H42.667v-42.667l68.446.001c-35.432-31.272-57.78-77.027-57.78-128c0-46.309 18.444-88.31 48.386-119.057" />
    </svg>
  );
}

export function ConfigSwitchIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M7 7h11" />
      <path d="m15 4 3 3-3 3" />
      <path d="M17 17H6" />
      <path d="m9 14-3 3 3 3" />
    </svg>
  );
}

export function AgentInstallIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 48 48" fill="none" aria-hidden="true">
      <path d="M0 0h48v48H0z" fill="none" />
      <path fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" d="M24 26v16.5m0-37V15m14.932 5.35L42.5 25.5l-14.932 5.65L24 26zm0 0L33 18" />
      <path fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" d="M38.932 26.85v8a2.895 2.895 0 0 1-1.87 2.708L23.998 42.5l-13.062-4.942a2.895 2.895 0 0 1-1.87-2.708v-8m20.184-10.1l5.5-5.5" />
      <path fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" d="M9.068 20.35L5.5 25.5l14.932 5.65L24 26Zm0 0L15 18m3.75-1.25l-5.5-5.5" />
    </svg>
  );
}

export function TrashIcon({ size = 13 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3 6h18" />
      <path d="M8 6V4h8v2" />
      <path d="M19 6l-1 14H6L5 6" />
      <path d="M10 11v5" />
      <path d="M14 11v5" />
    </svg>
  );
}

export function OnboardingGuideIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 2048 2048" aria-hidden="true">
      <path d="M0 0h2048v2048H0z" fill="none" />
      <path fill="currentColor" d="M2048 512v1536H0V512h517q-2-16-3-32t-2-32q0-93 35-174t96-143t142-96T960 0q93 0 174 35t143 96t96 142t35 175q0 16-1 32t-4 32zM960 128q-66 0-124 25t-102 69t-69 102t-25 124t25 124t68 102t102 69t125 25t124-25t101-68t69-102t26-125t-25-124t-69-101t-102-69t-124-26m960 512h-555q-25 52-62 97t-85 77q103 40 186 106t140 152t89 188t31 212v64h-128v-64q0-123-44-228t-121-183t-182-121t-229-44q-111 0-210 38t-176 107t-126 162t-61 205h648l-230-230l91-90l384 384l-384 384l-91-90l230-230H256v-64q0-110 31-211t90-187t141-152t185-107q-98-69-148-175H128v1280h1792z" />
    </svg>
  );
}

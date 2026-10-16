import React from "react";
import { useI18n } from "../../i18n/index";
import { LocalPanel } from "../file/ProjectAddPopover";
import { useSessionDirBrowser } from "./useSessionDirBrowser";

/**
 * 换会话工作目录的选路径弹层。
 *
 * 复用的是新建项目那套 LocalPanel（同一个组件、同一个 /api/local_dirs 端点），
 * 所以「随便填」与「点进去浏览」两条路都走同一份校验：路径不存在时端点返回 404，
 * 这里就把错误显示出来、确认按钮禁用 —— 不需要另加一个存在性检查接口。
 *
 * 刻意不复用 useProjectDirectoryBrowser：那个 hook 绑着 projectAddMode / 项目注册表刷新 /
 * 建 worktree 等一串只对「新建项目」成立的分支，在这里调它等于把那些副作用也带进来。
 * 这里只保留「浏览 + 选中一个目录」这半边。
 */
export function SessionDirectoryPicker({
  nodeId,
  defaultPath,
  busy,
  error,
  onCancel,
  onConfirm,
}: {
  nodeId?: string;
  defaultPath: string;
  busy: boolean;
  error: string;
  onCancel: () => void;
  onConfirm: (path: string) => void;
}) {
  const { t } = useI18n();
  const { state: localState, input, setInput, load } = useSessionDirBrowser(defaultPath, nodeId);

  const targetPath = localState.path;
  const canConfirm = !busy && !localState.loading && !localState.error && !!targetPath;

  return (
    <div style={overlayStyle} onClick={onCancel}>
      <div style={panelStyle} onClick={(event) => event.stopPropagation()}>
        <div style={{ fontSize: "13px", fontWeight: 600 }}>{t("session.dirPicker.title")}</div>
        <input
          value={input}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void load(input);
          }}
          onBlur={() => {
            if (input.trim() && input.trim() !== localState.path) void load(input);
          }}
          placeholder={t("session.dirPicker.placeholder")}
          aria-label={t("session.dirPicker.placeholder")}
          style={inputStyle}
        />
        <LocalPanel
          localState={localState}
          onLocalNavigate={(path) => void load(path)}
          onLocalSelect={() => undefined}
          onLocalAdd={() => onConfirm(targetPath)}
          localActionLabel={t("session.dirPicker.use")}
          localDisabledAddedRoot={false}
          localBrowseOnly
        />
        {error ? <div style={{ fontSize: "12px", color: "#dc2626", overflowWrap: "anywhere" }}>{error}</div> : null}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: "8px" }}>
          <button type="button" onClick={onCancel} style={secondaryButtonStyle}>
            {t("common.cancel")}
          </button>
          <button
            type="button"
            disabled={!canConfirm}
            onClick={() => onConfirm(targetPath)}
            style={{
              ...primaryButtonStyle,
              opacity: canConfirm ? 1 : 0.5,
              cursor: canConfirm ? "pointer" : "not-allowed",
            }}
          >
            {busy ? t("session.dirPicker.moving") : t("session.dirPicker.confirm")}
          </button>
        </div>
      </div>
    </div>
  );
}

const overlayStyle: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  zIndex: 1001,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: "16px",
  background: "rgba(15, 23, 42, 0.42)",
};

const panelStyle: React.CSSProperties = {
  width: "360px",
  maxWidth: "calc(100vw - 32px)",
  padding: "16px",
  borderRadius: "14px",
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  color: "var(--text-primary)",
  boxShadow: "0 18px 48px rgba(15, 23, 42, 0.24)",
  display: "flex",
  flexDirection: "column",
  gap: "12px",
};

const inputStyle: React.CSSProperties = {
  width: "100%",
  boxSizing: "border-box",
  borderRadius: "8px",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "8px 10px",
  outline: "none",
};

const primaryButtonStyle: React.CSSProperties = {
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
  borderRadius: "8px",
  padding: "8px 14px",
  fontSize: "12px",
  fontWeight: 600,
};

const secondaryButtonStyle: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: "8px",
  padding: "8px 14px",
  fontSize: "12px",
  cursor: "pointer",
};

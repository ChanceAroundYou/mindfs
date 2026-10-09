import type { I18nContextValue } from "../../i18n/index";

export type FileTreeRefreshButtonProps = {
  handleRefreshClick: () => unknown;
  setTreePressed: (pressed: boolean) => void;
  t: I18nContextValue["t"];
  treePressed: boolean;
  treeRefreshing: boolean;
};

/**
 * 项目树右上角的刷新按钮（2026-10-08 从 FileTree.tsx 抽出）。
 * 52 行 JSX 只闭包 5 个符号，是这个文件里最划算的一刀；按下/旋转两态的背景色
 * 都在这里，别再搬回宿主。
 */
export function FileTreeRefreshButton({
  handleRefreshClick, setTreePressed, t, treePressed, treeRefreshing,
}: FileTreeRefreshButtonProps) {
  return (
        <button
          type="button"
          data-onboarding="sidebar-refresh"
          onClick={() => void handleRefreshClick()}
          onMouseDown={() => setTreePressed(true)}
          onMouseUp={() => setTreePressed(false)}
          onMouseLeave={() => setTreePressed(false)}
          aria-label={t("common.refresh")}
          title={t("common.refresh")}
          style={{
            width: "22px",
            height: "28px",
            borderRadius: "8px",
            border: "none",
            background: "transparent",
            color: "var(--text-secondary)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "flex-end",
            cursor: "pointer",
            outline: "none",
            flexShrink: 0,
            padding: 0,
          }}
        >
          <span
            data-refresh-visual
            style={{
              width: "18px",
              height: "28px",
              borderRadius: "8px",
              background: treePressed || treeRefreshing ? "rgba(0, 0, 0, 0.06)" : "transparent",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              width="13"
              height="13"
              viewBox="0 0 24 24"
              aria-hidden="true"
              style={treeRefreshing ? { animation: "mindfs-update-spin 0.8s linear infinite" } : undefined}
            >
              <path
                fill="currentColor"
                d="M19.91 15.51h-4.53a1 1 0 0 0 0 2h2.4A8 8 0 0 1 4 12a1 1 0 0 0-2 0a10 10 0 0 0 16.88 7.23V21a1 1 0 0 0 2 0v-4.5a1 1 0 0 0-.97-.99M12 2a10 10 0 0 0-6.88 2.77V3a1 1 0 0 0-2 0v4.5a1 1 0 0 0 1 1h4.5a1 1 0 0 0 0-2h-2.4A8 8 0 0 1 20 12a1 1 0 0 0 2 0A10 10 0 0 0 12 2"
              />
            </svg>
          </span>
        </button>
  );
}

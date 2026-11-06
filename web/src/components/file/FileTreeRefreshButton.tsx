import type { I18nContextValue } from "../../i18n/index";
import { RefreshIcon } from "./icons";

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
            <RefreshIcon spinning={treeRefreshing} />
          </span>
        </button>
  );
}

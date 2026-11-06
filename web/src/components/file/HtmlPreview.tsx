import React, { memo } from "react";
import { useRawAssetUrl } from "../../hooks/useRawAssetUrl";
import { useI18n } from "../../i18n/index";

type HtmlPreviewProps = {
  path: string;
  root?: string;
  /** 文件版本（mtime）：带上它，重新生成的同名页面才会真的重新加载。 */
  version?: string;
};

/**
 * HTML 网页预览：沙箱 iframe 里渲染。
 *
 * sandbox="allow-scripts" **不给** allow-same-origin —— 文档落在 opaque origin，
 * 页面里的 JS 能跑、能交互，但读不到 mindfs 的 localStorage，也碰不到父页面。
 * 后端对 text/html 响应另加 CSP sandbox（直接在新标签页打开 URL 也一样隔离）。
 */
function HtmlPreviewInner({ path, root, version }: HtmlPreviewProps) {
  const { t } = useI18n();
  const url = useRawAssetUrl(root, path, undefined, version);
  if (!url) {
    return <div className="document-preview-message">{t("fileViewer.previewFailed")}</div>;
  }
  return (
    <div className="document-preview-html">
      <iframe
        src={url}
        title={path}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        style={{ width: "100%", height: "100%", border: "none", background: "#fff" }}
      />
    </div>
  );
}

export const HtmlPreview = memo(HtmlPreviewInner, (prev, next) => (
  prev.path === next.path && prev.root === next.root && prev.version === next.version
));

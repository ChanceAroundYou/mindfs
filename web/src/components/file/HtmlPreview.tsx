import React, { memo } from "react";
import { useHtmlPreviewFrame } from "../../hooks/useHtmlPreviewFrame";
import { useI18n } from "../../i18n/index";

type HtmlPreviewProps = {
  path: string;
  root?: string;
  /** 文件版本（mtime）：带上它，重新生成的同名页面才会真的重新加载。 */
  version?: string;
  /** 文件内容：只用于静态体检（识别构建前的开发入口），不参与渲染。 */
  content?: string;
};

/**
 * HTML 网页预览：沙箱 iframe 里渲染。
 *
 * sandbox 带 allow-scripts **和 allow-same-origin**（用户 2026-11-09 拍板）：
 * 页面像普通标签页一样跑，localStorage / IndexedDB / serviceWorker 都可用。
 * 代价是它与 mindfs 同源、能碰 parent.document —— 预览自己的项目文件是本工具的
 * 既定用法，边界按「信任本地项目内容」处理。后端对 text/html 的响应头
 * （CSP: sandbox allow-scripts allow-same-origin）必须与这里保持一致。
 *
 * 白屏不再是黑盒：开发入口（引用 .tsx 等浏览器执行不了的源文件）、iframe 里的
 * 脚本报错/资源失败、以及「加载完成但没有任何可见内容」都会在顶部提示条里说明。
 */
function HtmlPreviewInner({ path, root, version, content }: HtmlPreviewProps) {
  const { t } = useI18n();
  const { url, unrunnableScripts, problems, blank, frameRef, handleFrameLoad } = useHtmlPreviewFrame({
    root,
    path,
    version,
    content,
  });

  if (!url) {
    return <div className="document-preview-message">{t("fileViewer.previewFailed")}</div>;
  }

  const notice = unrunnableScripts.length
    ? { tone: "warning", text: t("fileViewer.htmlDevEntry", { files: unrunnableScripts.join(", ") }) }
    : problems.length
      ? { tone: "error", text: t("fileViewer.htmlRuntimeError", { reason: problems[0] }) }
      : blank
        ? { tone: "muted", text: t("fileViewer.htmlBlank") }
        : null;

  return (
    <div className="document-preview-html">
      {notice ? (
        <div className={`document-preview-html-notice is-${notice.tone}`} role="status">
          {notice.text}
        </div>
      ) : null}
      <iframe
        ref={frameRef}
        src={url}
        title={path}
        sandbox="allow-scripts allow-same-origin"
        referrerPolicy="no-referrer"
        onLoad={handleFrameLoad}
        style={{ width: "100%", flex: "1 1 auto", minHeight: 0, border: "none", background: "#fff" }}
      />
    </div>
  );
}

export const HtmlPreview = memo(HtmlPreviewInner, (prev, next) => (
  prev.path === next.path
  && prev.root === next.root
  && prev.version === next.version
  && prev.content === next.content
));

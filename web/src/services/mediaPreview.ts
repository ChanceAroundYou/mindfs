/**
 * 预览类型判定：HTML 网页与视频。
 *
 * 与 documentPreview.ts（pdf/word/excel/pptx）分开：这里的两个不是「文档」，
 * 渲染路径也完全不同（沙箱 iframe / <video>）。
 */

const HTML_EXTENSIONS: ReadonlySet<string> = new Set([".html", ".htm"]);

/** 浏览器能直接解的容器（能不能真解出来看编码，失败时组件会回落成「请下载」）。 */
const PLAYABLE_VIDEO_EXTENSIONS: ReadonlySet<string> = new Set([
  ".mp4",
  ".m4v",
  ".webm",
  ".ogv",
  ".mov",
]);

/** HTML 网页：走沙箱 iframe 渲染。 */
export function isHtmlPreview(ext: string, mime = ""): boolean {
  const normalizedExt = ext.trim().toLowerCase();
  if (HTML_EXTENSIONS.has(normalizedExt)) {
    return true;
  }
  return mime.trim().toLowerCase() === "text/html";
}

/**
 * 视频：playable = 交给 <video> 播；unsupported = 是视频但浏览器解不了
 * （.avi/.wmv/.flv 这类），给下载入口，不假装能播；null = 不是视频。
 */
export function classifyVideo(ext: string, mime = ""): "playable" | "unsupported" | null {
  const normalizedExt = ext.trim().toLowerCase();
  if (PLAYABLE_VIDEO_EXTENSIONS.has(normalizedExt)) {
    return "playable";
  }
  const normalizedMime = mime.trim().toLowerCase();
  if (!normalizedMime.startsWith("video/")) {
    return null;
  }
  return "unsupported";
}

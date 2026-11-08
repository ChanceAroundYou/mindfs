/**
 * HTML 预览的静态体检（纯函数，无 DOM / IO）。
 *
 * 浏览器只能执行 JavaScript。页面引用 .ts / .tsx / .jsx / .vue / .svelte 源文件，
 * 或引用 Vite 开发服务器注入的 /@vite/ 、/@react-refresh 时，预览必然是白屏 ——
 * 这不是沙箱的问题，而是「构建前的开发入口」本身不可运行。识别出来给明确提示，
 * 比让用户对着白屏猜要强。
 */

const NON_BROWSER_SCRIPT_PATTERN = /\.(?:tsx?|jsx|vue|svelte)$/i;

const DEV_SERVER_MARKERS = ["/@vite/", "/@react-refresh", "/@fs/"];

const SCRIPT_SRC_PATTERN = String.raw`<script\b[^>]*?\bsrc\s*=\s*(["'])(.*?)\1[^>]*>`;

/** 返回页面里浏览器执行不了的脚本 URL（去重、保序、最多 5 条）。 */
export function findUnrunnableScripts(html: string | null | undefined): string[] {
  if (!html) {
    return [];
  }
  const pattern = new RegExp(SCRIPT_SRC_PATTERN, "gi");
  const found: string[] = [];
  let match: RegExpExecArray | null = null;
  while ((match = pattern.exec(html)) !== null) {
    const src = String(match[2] || "").trim();
    if (!src) {
      continue;
    }
    const pathPart = src.split(/[?#]/, 1)[0].toLowerCase();
    const lowerSrc = src.toLowerCase();
    const isDevServer = DEV_SERVER_MARKERS.some((marker) => lowerSrc.includes(marker));
    if (!isDevServer && !NON_BROWSER_SCRIPT_PATTERN.test(pathPart)) {
      continue;
    }
    if (!found.includes(src)) {
      found.push(src);
    }
    if (found.length >= 5) {
      break;
    }
  }
  return found;
}

/** 是否是「构建前入口」：引用了浏览器执行不了的脚本。 */
export function isDevEntryHtml(html: string | null | undefined): boolean {
  return findUnrunnableScripts(html).length > 0;
}

import { translateNow } from "../i18n";
import { DEPLOY_PREFIX } from "../services/net/prefix";
import { shouldReloadForStaleAsset } from "../services/platform/staleAssetRecovery";



function readableAssetPath(raw: string): string {
  const value = String(raw || "").trim();
  if (!value) {
    return "unknown resource";
  }
  try {
    const url = new URL(value, window.location.href);
    return `${url.pathname}${url.search}`;
  } catch {
    return value;
  }
}



function mindFSAssetPath(raw: string): string {
  const value = String(raw || "").trim();
  if (!value) {
    return "";
  }
  try {
    const url = new URL(value, window.location.href);
    if (url.origin !== window.location.origin) {
      return "";
    }
    const pathname = url.pathname;
    // 部署前缀下所有静态资源挂在 DEPLOY_PREFIX/ 之下（空前缀即根 /）
    const assetRoot = DEPLOY_PREFIX === "" ? "/" : `${DEPLOY_PREFIX}/`;
    if (!pathname.startsWith(assetRoot)) {
      return "";
    }
    const rest = pathname.slice(assetRoot.length);
    const knownAsset =
      /^assets\/[^/]+\.(?:js|css)$/i.test(rest) ||
      /^(?:pwa(?:-|_)[^/]*|favicon)\.(?:png|svg)$/i.test(rest) ||
      rest === "manifest.webmanifest" ||
      rest === "apple-touch-icon.png" ||
      rest === "service-worker.js" ||
      rest === "offline.html";
    if (!knownAsset) {
      return "";
    }
    return `${pathname}${url.search}`;
  } catch {
    return "";
  }
}



function showFrontendAssetMissingNotice(rawPath: string): void {
  if (typeof document === "undefined") {
    return;
  }
  const path = readableAssetPath(rawPath);
  const id = "mindfs-asset-missing-notice";
  const existing = document.getElementById(id);
  if (existing) {
    const message = existing.querySelector("[data-message]");
    if (message) {
      message.textContent = translateNow("asset.missing", { path });
    }
    return;
  }

  const notice = document.createElement("div");
  notice.id = id;
  notice.setAttribute("role", "alert");
  notice.style.position = "fixed";
  notice.style.left = "12px";
  notice.style.right = "12px";
  notice.style.top = "12px";
  notice.style.zIndex = "2147483647";
  notice.style.display = "flex";
  notice.style.alignItems = "center";
  notice.style.justifyContent = "space-between";
  notice.style.gap = "12px";
  notice.style.padding = "10px 12px";
  notice.style.border = "1px solid rgba(185, 28, 28, 0.35)";
  notice.style.borderRadius = "8px";
  notice.style.background = "#fff5f5";
  notice.style.color = "#7f1d1d";
  notice.style.boxShadow = "0 10px 30px rgba(15, 23, 42, 0.18)";
  notice.style.fontFamily = "system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif";
  notice.style.fontSize = "13px";
  notice.style.lineHeight = "1.4";

  const message = document.createElement("div");
  message.dataset.message = "1";
  message.style.minWidth = "0";
  message.style.overflowWrap = "anywhere";
  message.textContent = translateNow("asset.missing", { path });

  const reload = document.createElement("button");
  reload.type = "button";
  reload.textContent = translateNow("common.refresh");
  reload.style.border = "1px solid rgba(127, 29, 29, 0.3)";
  reload.style.borderRadius = "6px";
  reload.style.background = "#ffffff";
  reload.style.color = "#7f1d1d";
  reload.style.padding = "5px 9px";
  reload.style.cursor = "pointer";
  reload.style.flex = "0 0 auto";
  reload.onclick = () => window.location.reload();

  notice.append(message, reload);
  document.body.appendChild(notice);
}



/** 正在输入时不重载：重载会丢掉未发送的草稿，宁可只弹横幅让用户自己挑时机。 */
function isEditingText(): boolean {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement)) {
    return false;
  }
  return (
    active.isContentEditable ||
    active instanceof HTMLTextAreaElement ||
    active instanceof HTMLInputElement
  );
}



/**
 * 资源加载失败且像是哈希错配（旧页面加载已被换掉的 chunk）时，自动重载一次拿新壳子。
 * 返回 true 表示已发起重载，调用方不必再弹横幅。
 */
function recoverFromStaleAsset(path: string): boolean {
  if (typeof window === "undefined" || isEditingText()) {
    return false;
  }
  let storage: Pick<Storage, "getItem" | "setItem"> | null = null;
  try {
    storage = window.sessionStorage;
  } catch {
    storage = null;
  }
  if (!storage || !shouldReloadForStaleAsset(path, storage)) {
    return false;
  }
  window.location.reload();
  return true;
}



function resourceURLFromEventTarget(target: EventTarget | null): string {
  if (!(target instanceof HTMLElement)) {
    return "";
  }
  const value =
    target.getAttribute("src") ||
    target.getAttribute("href") ||
    target.getAttribute("poster") ||
    "";
  return mindFSAssetPath(value);
}



function dynamicImportFailureURL(reason: unknown): string {
  const message = reason instanceof Error ? reason.message : String(reason || "");
  if (!/dynamically imported module|module script|loading chunk|import/i.test(message)) {
    return "";
  }
  const match = message.match(/https?:\/\/[^\s"'<>]+|\/[^\s"'<>]+\.js(?:\?[^\s"'<>]+)?/);
  if (!match?.[0]) {
    return "dynamic module";
  }
  return mindFSAssetPath(match[0]);
}

export function installAssetRecoveryHandlers(): void {
  if (typeof window === "undefined") return;

  window.addEventListener("error", (event) => {
    const resourceURL = resourceURLFromEventTarget(event.target);
    if (resourceURL && !recoverFromStaleAsset(resourceURL)) {
      showFrontendAssetMissingNotice(resourceURL);
    }
    console.error("[global-error]", {
      message: event.message,
      filename: event.filename,
      lineno: event.lineno,
      colno: event.colno,
      error: event.error instanceof Error ? {
        name: event.error.name,
        message: event.error.message,
        stack: event.error.stack,
      } : event.error,
    });
  }, true);

  window.addEventListener("unhandledrejection", (event) => {
    const reason = event.reason;
    const resourceURL = dynamicImportFailureURL(reason);
    if (resourceURL && !recoverFromStaleAsset(resourceURL)) {
      showFrontendAssetMissingNotice(resourceURL);
    }
    console.error("[unhandled-rejection]", reason instanceof Error ? {
      name: reason.name,
      message: reason.message,
      stack: reason.stack,
    } : reason);
  });
}

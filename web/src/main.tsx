import { createRoot } from "react-dom/client";
import "./index.css";
import { App } from "./App";
import { registerServiceWorker } from "./registerServiceWorker";
import { applyAppearanceMode, getAppearanceMode } from "./services/prefs/appearance";
import { Login } from "./components/account/Login";
import { AuthGate } from "./components/account/AuthGate";
import { addNode, getNodes, setActiveNodeId } from "./services/net/nodeRegistry";
import { I18nProvider } from "./i18n";
import { installReloadObserver } from "./services/platform/reloadObserver";
import { installAssetRecoveryHandlers } from "./app/assetRecovery";
import { useNativeShellLifecycle } from "./app/useNativeShellLifecycle";

// 先记录启动情况，避免首屏异常导致无法留下重载线索。
installReloadObserver();

applyAppearanceMode();
if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const syncSystemAppearance = () => {
    if (getAppearanceMode() === "system") applyAppearanceMode("system");
  };
  if (typeof media.addEventListener === "function") {
    media.addEventListener("change", syncSystemAppearance);
  } else {
    media.addListener(syncSystemAppearance);
  }
}

function AppRoot() {
  const { ready, goToLauncher } = useNativeShellLifecycle();

  if (!ready) {
    return (
      <Login
        onOpenNode={async (nodeURL) => {
          try {
            const url = String(nodeURL || "").trim().replace(/\/+$/, "");
            if (!url) return;
            const name = (() => {
              try {
                return new URL(url).hostname;
              } catch {
                return url;
              }
            })();
            const nodes = getNodes();
            const existing = nodes.find((node) => node.url === url);
            if (existing) {
              setActiveNodeId(existing.id);
              window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
              return;
            }
            const node = await addNode({ name, url });
            setActiveNodeId(node.id);
            window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
          } catch {
            window.location.assign(nodeURL);
          }
        }}
      />
    );
  }

  return (
    <AuthGate>
      <App onGoHome={goToLauncher} />
    </AuthGate>
  );
}

const container = document.getElementById("root");
if (container) {
  createRoot(container).render(
    <I18nProvider>
      <AppRoot />
    </I18nProvider>,
  );
}

registerServiceWorker();
installAssetRecoveryHandlers();

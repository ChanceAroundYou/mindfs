import { shouldRegisterServiceWorker } from "./services/runtime";

export function registerServiceWorker(): void {
  if (typeof window === "undefined") {
    return;
  }
  if (!("serviceWorker" in navigator)) {
    return;
  }
  if (!shouldRegisterServiceWorker()) {
    navigator.serviceWorker.getRegistrations()
      .then((registrations) => {
        registrations.forEach((registration) => {
          void registration.unregister();
        });
      })
      .catch((error: unknown) => {
        console.error("service worker unregister failed", error);
      });
    return;
  }
  if (import.meta.env.DEV) {
    return;
  }

  const serviceWorkerURL = new URL("service-worker.js", window.location.href);

  window.addEventListener("load", () => {
    navigator.serviceWorker.register(serviceWorkerURL, { scope: "./" }).catch((error: unknown) => {
      console.error("service worker registration failed", error);
    });
  });
}

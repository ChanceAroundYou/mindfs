import React from "react";
import { getBaseCatalog } from "./viewCatalog";

// registry 依赖 @json-render/react + shadcn，异步构建（见 viewCatalog 的懒加载说明）。
let registryPromise: Promise<unknown> | null = null;

export function getRegistry(): Promise<unknown> {
  if (!registryPromise) {
    registryPromise = (async () => {
      const [{ defineRegistry }, { shadcnComponents }, baseCatalog] =
        await Promise.all([
          import("@json-render/react"),
          import("@json-render/shadcn"),
          getBaseCatalog(),
        ]);

      function ResponsiveDialog(props: any) {
        const isMobile =
          typeof window !== "undefined" &&
          window.matchMedia("(max-width: 767px)").matches;
        const Component = isMobile
          ? (shadcnComponents as any).Drawer
          : (shadcnComponents as any).Dialog;
        return <Component {...props} />;
      }

      const { registry } = defineRegistry(baseCatalog as any, {
        components: {
          ...(shadcnComponents as any),
          Dialog: ResponsiveDialog,
        } as any,
        actions: {
          // Runtime actions are provided by JSONUIProvider handlers in App.tsx.
          navigate: async () => {},
        } as any,
      });
      return registry;
    })();
  }
  return registryPromise;
}

import React, { lazy, memo, Suspense, useEffect, useMemo, useState } from "react";
import { getRegistry } from "./registry";

type RendererProps = {
  tree: {
    root: string;
    elements: Record<string, unknown>;
  };
  initialState?: Record<string, unknown>;
  handlers?: Record<string, (params: Record<string, unknown>) => void | Promise<void>>;
};

function normalizeTreeSpec(tree: RendererProps["tree"]): RendererProps["tree"] {
  const elements = tree?.elements || {};
  const normalized: Record<string, unknown> = {};
  Object.entries(elements).forEach(([key, value]) => {
    if (!value || typeof value !== "object") {
      normalized[key] = value;
      return;
    }
    const element = value as Record<string, unknown>;
    normalized[key] = {
      ...element,
      props: element.props && typeof element.props === "object" ? element.props : {},
    };
  });
  return { ...tree, elements: normalized };
}

// @json-render 渲染组件懒加载，首次渲染 JSON UI 时才拉取对应 chunk。
const JsonRenderer = lazy(() =>
  import("@json-render/react").then((m) => ({ default: m.Renderer })),
);
const JSONUIProvider = lazy(() =>
  import("@json-render/react").then((m) => ({ default: m.JSONUIProvider })),
);

function RendererInner({ tree, initialState = {}, handlers = {} }: RendererProps) {
  const spec = useMemo(() => normalizeTreeSpec(tree), [tree]);
  const [registry, setRegistry] = useState<unknown>(null);

  useEffect(() => {
    let cancelled = false;
    getRegistry()
      .then((r) => {
        if (!cancelled) setRegistry(r);
      })
      .catch(() => {
        if (!cancelled) setRegistry(undefined);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!registry) return null;
  return (
    <Suspense fallback={null}>
      <JSONUIProvider registry={registry as any} initialState={initialState} handlers={handlers}>
        <JsonRenderer spec={spec as any} registry={registry as any} />
      </JSONUIProvider>
    </Suspense>
  );
}

export const Renderer = memo(RendererInner, (prev, next) => (
  prev.tree === next.tree &&
  prev.initialState === next.initialState &&
  prev.handlers === next.handlers
));

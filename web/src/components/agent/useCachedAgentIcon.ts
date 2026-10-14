import { useEffect, useState } from "react";
import { appPath } from "../../services/net/base";

const iconCache = new Map<string, string>();
const inflight = new Map<string, Promise<string>>();

function svgToDataURL(svg: string): string {
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;
}

async function loadIconOnce(url: string): Promise<string> {
  const cached = iconCache.get(url);
  if (cached) return cached;
  const pending = inflight.get(url);
  if (pending) return pending;

  const task = (async () => {
    const response = await fetch(url, { cache: "force-cache" });
    if (!response.ok) throw new Error(`failed to load icon: ${response.status}`);
    const dataURL = svgToDataURL(await response.text());
    iconCache.set(url, dataURL);
    return dataURL;
  })();
  inflight.set(url, task);
  try {
    return await task;
  } finally {
    inflight.delete(url);
  }
}

export function useCachedAgentIcon(fileName?: string): string | undefined {
  const url = fileName ? appPath(`/assets/agents/${fileName}`) : undefined;
  const [src, setSrc] = useState<string | undefined>(() => {
    if (!url) return undefined;
    if (!url.endsWith(".svg")) return url;
    return iconCache.get(url);
  });

  useEffect(() => {
    let cancelled = false;
    if (!url || !url.endsWith(".svg")) {
      setSrc(url);
      return;
    }
    const cached = iconCache.get(url);
    if (cached) {
      setSrc(cached);
      return;
    }
    loadIconOnce(url)
      .then((dataURL) => {
        if (!cancelled) setSrc(dataURL);
      })
      .catch(() => {
        if (!cancelled) setSrc(url);
      });
    return () => {
      cancelled = true;
    };
  }, [url]);

  return src;
}

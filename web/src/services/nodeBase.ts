export function normalizeExplicitNodeBase(input: string): string {
  return String(input || "").trim().replace(/\/+$/, "");
}

export function deriveLocalNodeBase(origin: string, deployPrefix: string): string {
  const base = normalizeExplicitNodeBase(origin);
  const prefix = String(deployPrefix || "").trim().replace(/\/+$/, "");
  if (!base || !prefix) return base;
  return `${base}${prefix.startsWith("/") ? prefix : `/${prefix}`}`;
}

export function repairDuplicateDeployPrefix(input: string, deployPrefix: string): string {
  const base = normalizeExplicitNodeBase(input);
  const prefix = String(deployPrefix || "").trim().replace(/\/+$/, "");
  if (!base || !prefix) return base;
  const normalizedPrefix = prefix.startsWith("/") ? prefix : `/${prefix}`;
  try {
    const url = new URL(base);
    const repeated = `${normalizedPrefix}${normalizedPrefix}`;
    if (url.pathname === repeated || url.pathname.startsWith(`${repeated}/`)) {
      url.pathname = `${normalizedPrefix}${url.pathname.slice(repeated.length)}`;
    }
    return normalizeExplicitNodeBase(url.toString());
  } catch {
    return base;
  }
}

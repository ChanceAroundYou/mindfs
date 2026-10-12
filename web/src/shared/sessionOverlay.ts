/** Minimal exchange shape needed by the session window overlay. */
export type OverlayExchange = {
  seq?: number | string | null;
  role?: string;
  content?: string;
  mindfs_id?: string;
} & Record<string, unknown>;

export interface TailOverlayInput {
  /** App cache, including persisted rows and in-flight entries. */
  exchanges?: readonly OverlayExchange[] | null;
  /** Persisted rows currently rendered by the window. */
  visibleExchanges?: readonly OverlayExchange[] | null;
}

const seqOf = (ex: OverlayExchange | undefined): number => Number(ex?.seq || 0);
const roleOf = (ex: OverlayExchange | undefined): string =>
  String(ex?.role || "").toLowerCase();
const mindfsIDOf = (ex: OverlayExchange | undefined): string =>
  String(ex?.mindfs_id || "").trim();

const roleCategory = (role: string): string => {
  if (role === "user") return "user";
  if (role === "tool") return "tool";
  if (
    role === "agent" ||
    role === "assistant" ||
    role === "thought" ||
    role === "plan" ||
    role === "todo" ||
    role === "compact"
  ) {
    return "agent";
  }
  return role;
};

const identityOf = (ex: OverlayExchange): string => {
  const mindfsID = mindfsIDOf(ex);
  if (!mindfsID) return "";
  return JSON.stringify([roleCategory(roleOf(ex)), mindfsID]);
};

/**
 * Return cached rows the active window does not already render.
 *
 * G-BH: persisted rows are reconciled by seq; transient rows are reconciled
 * only by MindFS identity and role category. Text similarity is not identity.
 * Tool cards retain their own callId reconciliation in the timeline.
 */
export function computeTailOverlay(input: TailOverlayInput): OverlayExchange[] {
  const exchanges = Array.isArray(input.exchanges) ? input.exchanges : [];
  const visible = Array.isArray(input.visibleExchanges) ? input.visibleExchanges : [];
  const visibleSeqs = new Set<number>();
  const renderedIdentities = new Set<string>();

  for (const ex of [...visible, ...exchanges]) {
    if (seqOf(ex) > 0) {
      const identity = identityOf(ex);
      if (identity) renderedIdentities.add(identity);
    }
  }
  for (const ex of visible) {
    const seq = seqOf(ex);
    if (seq > 0) visibleSeqs.add(seq);
  }

  const out: OverlayExchange[] = [];
  for (const ex of exchanges) {
    if (seqOf(ex) > 0) {
      if (!visibleSeqs.has(seqOf(ex))) out.push(ex);
      continue;
    }

    // Tool cards have their own callId reconciliation in the timeline.
    if (roleOf(ex) === "tool") {
      out.push(ex);
      continue;
    }

    const identity = identityOf(ex);
    if (identity && renderedIdentities.has(identity)) continue;
    out.push(ex);
  }
  return out;
}

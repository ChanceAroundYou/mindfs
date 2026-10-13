/** Minimal session fields rendered on task cards; the full app session model stays in appSession. */
export type SessionPresentationSummary = {
  name?: string;
  agent?: string;
  pending?: boolean;
};

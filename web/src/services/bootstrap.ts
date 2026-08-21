export type BootstrapPhase = "ready" | "needs_pairing"; // ponytail: needs_pairing kept for dead-code compat

export type BootstrapState = {
  phase: BootstrapPhase;
  error: string;
  // ponytail: compat — e2ee removed but keep field so old subscribers don't get undefined
  e2ee: { configured: boolean; required: boolean; nodeId: string; secretPresent: boolean; unlocked: boolean };
};

type BootstrapListener = (state: BootstrapState) => void;

class BootstrapService {
  private state: BootstrapState = { phase: "ready", error: "", e2ee: { configured: true, required: false, nodeId: "", secretPresent: false, unlocked: false } };
  private listeners = new Set<BootstrapListener>();

  subscribe(listener: BootstrapListener) {
    this.listeners.add(listener);
    try { listener(this.snapshot()); } catch {}
    return () => { this.listeners.delete(listener); }
  }

  snapshot(): BootstrapState {
    return { ...this.state };
  }

  canUseProtectedAPI(): boolean {
    return true;
  }

  async start(): Promise<BootstrapState> {
    this.setState({ phase: "ready", error: "" });
    return this.snapshot();
  }

  async submitPairingSecret(_secret?: string): Promise<BootstrapState> {
    return this.snapshot();
  }

  private setState(patch: Partial<BootstrapState>) {
    this.state = { ...this.state, ...patch };
    this.emit();
  }

  private emit() {
    const snapshot = this.snapshot();
    this.listeners.forEach((l) => l(snapshot));
  }
}

export const bootstrapService = new BootstrapService();

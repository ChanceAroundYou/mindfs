export type BootstrapPhase = "ready";

export type BootstrapState = {
  phase: BootstrapPhase;
  error: string;
};

type BootstrapListener = (state: BootstrapState) => void;

class BootstrapService {
  private state: BootstrapState = { phase: "ready", error: "" };
  private listeners = new Set<BootstrapListener>();

  subscribe(listener: BootstrapListener) {
    this.listeners.add(listener);
    listener(this.snapshot());
    return () => this.listeners.delete(listener);
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

  async submitPairingSecret(): Promise<BootstrapState> {
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

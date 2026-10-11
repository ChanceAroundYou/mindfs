export type BootstrapPhase = "idle" | "pending" | "ready" | "error";

export type BootstrapState = {
  phase: BootstrapPhase;
  error: string;
};

type BootstrapListener = (state: BootstrapState) => void;

class BootstrapService {
  private state: BootstrapState = {
    phase: "idle",
    error: "",
  };
  private listeners = new Set<BootstrapListener>();
  private startPromise: Promise<BootstrapState> | null = null;

  subscribe(listener: BootstrapListener) {
    this.listeners.add(listener);
    listener(this.snapshot());
    return () => {
      this.listeners.delete(listener);
    };
  }

  snapshot(): BootstrapState {
    return { ...this.state };
  }

  canUseProtectedAPI(): boolean {
    const state = this.snapshot();
    return state.phase === "ready";
  }

  async start(): Promise<BootstrapState> {
    if (this.state.phase === "ready") {
      return this.snapshot();
    }
    if (this.startPromise) {
      return this.startPromise;
    }
    this.startPromise = this.runStart();
    try {
      return await this.startPromise;
    } finally {
      this.startPromise = null;
    }
  }

  private async runStart(): Promise<BootstrapState> {
    this.setState({ phase: "pending", error: "" });
    try {
      this.setState({ phase: "ready", error: "" });
      return this.snapshot();
    } catch (err) {
      this.setState({
        phase: "error",
        error: err instanceof Error ? err.message : "bootstrap_failed",
      });
      return this.snapshot();
    }
  }

  private setState(patch: Partial<BootstrapState>) {
    this.state = {
      ...this.state,
      ...patch,
    };
    this.emit();
  }

  private emit() {
    const snapshot = this.snapshot();
    this.listeners.forEach((listener) => listener(snapshot));
  }
}

export const bootstrapService = new BootstrapService();

import { useEffect, useState } from "react";
import { bootstrapService } from "../../services/net/bootstrap";

export function useFileTreeProtectedAPI(): boolean {
  const [ready, setReady] = useState(() => bootstrapService.canUseProtectedAPI());

  useEffect(() => bootstrapService.subscribe(() => {
    setReady(bootstrapService.canUseProtectedAPI());
  }), []);

  return ready;
}

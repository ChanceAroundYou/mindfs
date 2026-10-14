import { useEffect, useState } from "react";
import { sessionService } from "../../services/session";
import { recentQuickSwitchGroups, ringGesture } from "../../services/quickSwitch";

export { ringGesture };

export function useSessionRingQuickSwitch(open: boolean) {
  const [groups, setGroups] = useState<ReturnType<typeof recentQuickSwitchGroups>>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open) return;
    let active = true;
    setLoading(true);
    void sessionService.fetchMultiRootSessions(3)
      .then((items) => {
        if (!active) return;
        setGroups(recentQuickSwitchGroups(items));
        setLoading(false);
      })
      .catch(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [open]);

  return { groups, loading };
}

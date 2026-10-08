import React from "react";

export function useAgentConfigOutsideClick({ agentConfigFlow, agentConfigStep, agentLifecycleOpen, agentConfigPopoverRef, agentLifecyclePopoverRef, closeAgentConfigFlow, closeAgentLifecycleFlow }: any) {
React.useEffect(() => {
  if (!agentConfigFlow || agentConfigStep !== "agent") {
    return;
  }
  const handlePointerDown = (event: MouseEvent) => {
    if (!agentConfigPopoverRef.current?.contains(event.target as Node)) {
      closeAgentConfigFlow();
    }
  };
  document.addEventListener("mousedown", handlePointerDown);
  return () => document.removeEventListener("mousedown", handlePointerDown);
}, [agentConfigFlow, agentConfigStep, closeAgentConfigFlow]);

React.useEffect(() => {
  if (!agentLifecycleOpen) {
    return;
  }
  const handlePointerDown = (event: MouseEvent) => {
    if (!agentLifecyclePopoverRef.current?.contains(event.target as Node)) {
      closeAgentLifecycleFlow();
    }
  };
  document.addEventListener("mousedown", handlePointerDown);
  return () => document.removeEventListener("mousedown", handlePointerDown);
}, [agentLifecycleOpen, closeAgentLifecycleFlow]);
}

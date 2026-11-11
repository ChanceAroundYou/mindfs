import React from "react";

export function useAgentConfigOutsideClick({ agentConfigFlow, agentLifecycleOpen, agentConfigPopoverRef, agentLifecyclePopoverRef, closeAgentConfigFlow, closeAgentLifecycleFlow }: any) {
// 整条流程（harness 列表 / 详情 / 确认）都要点外关闭。此前只在 step=agent
// 时挂监听，进入详情后点空白处没反应，用户会以为浮层卡住了。
React.useEffect(() => {
  if (!agentConfigFlow) {
    return;
  }
  const handlePointerDown = (event: MouseEvent) => {
    if (!agentConfigPopoverRef.current?.contains(event.target as Node)) {
      closeAgentConfigFlow();
    }
  };
  document.addEventListener("mousedown", handlePointerDown);
  return () => document.removeEventListener("mousedown", handlePointerDown);
}, [agentConfigFlow, closeAgentConfigFlow]);

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

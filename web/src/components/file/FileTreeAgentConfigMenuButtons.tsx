import React from "react";
import {
  AgentInstallIcon,
  ConfigArchiveIcon,
  ConfigSwitchIcon,
  fileTreeMenuButtonStyle,
} from "./icons";

// 从 FileTreeHeaderOverlays 拆出（硬线 500 行）：菜单里的 agent 配置三连。
// 纯展示，回调由调用方给，按钮顺序与文案不变。
type FileTreeAgentConfigMenuButtonsProps = {
  t: (key: string) => string;
  openAgentConfigFlow: (kind: "backup" | "switch") => void;
  openAgentLifecycleFlow: () => void;
};

export function FileTreeAgentConfigMenuButtons({
  t,
  openAgentConfigFlow,
  openAgentLifecycleFlow,
}: FileTreeAgentConfigMenuButtonsProps) {
  return (
    <>
      <button
        type="button"
        onClick={() => openAgentConfigFlow("backup")}
        style={fileTreeMenuButtonStyle}
      >
        <ConfigArchiveIcon />
        <span>{t("fileTree.addAgentConfig")}</span>
      </button>
      <button
        type="button"
        onClick={() => openAgentConfigFlow("switch")}
        style={fileTreeMenuButtonStyle}
      >
        <ConfigSwitchIcon />
        <span>{t("fileTree.switchAgentConfig")}</span>
      </button>
      <button
        type="button"
        onClick={openAgentLifecycleFlow}
        style={fileTreeMenuButtonStyle}
      >
        <AgentInstallIcon />
        <span>{t("fileTree.agentInstallUpdate")}</span>
      </button>
    </>
  );
}

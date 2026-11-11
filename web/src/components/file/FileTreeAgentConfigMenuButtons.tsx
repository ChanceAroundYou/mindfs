import React from "react";
import {
  AgentInstallIcon,
  ConfigArchiveIcon,
  ConfigSwitchIcon,
  fileTreeMenuButtonStyle,
} from "./icons";

// 从 FileTreeHeaderOverlays 拆出（硬线 500 行）：菜单里的 agent 配置入口。
// 只保留三个按钮：配置（含切换 / 备份 / 同步）、供应商与模型、安装和更新。
// 前两个共用同一层级——先选 harness，再进入各自的详情。
type FileTreeAgentConfigMenuButtonsProps = {
  t: (key: string) => string;
  openAgentConfigFlow: (kind: "config" | "provider") => void;
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
        onClick={() => openAgentConfigFlow("config")}
        style={fileTreeMenuButtonStyle}
      >
        <ConfigArchiveIcon />
        <span>{t("fileTree.agentConfig")}</span>
      </button>
      <button
        type="button"
        onClick={() => openAgentConfigFlow("provider")}
        style={fileTreeMenuButtonStyle}
      >
        <ConfigSwitchIcon />
        <span>{t("fileTree.agentProviders")}</span>
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
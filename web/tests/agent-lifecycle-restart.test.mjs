import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

test("agent-lifecycle-restart", async () => {
const fileTree = readFileSync(new URL("../src/components/FileTree.tsx", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const zhCN = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");
const enUS = readFileSync(new URL("../src/i18n/locales/en-US.ts", import.meta.url), "utf8");

assert.match(
  zhCN,
  /"fileTree\.agentInstallUpdate": "Agent 安装和更新"/,
  "Chinese install/update menu label should not mention restart",
);

assert.match(
  zhCN,
  /"fileTree\.switchAgentConfig": "Agent 配置切换重启"/,
  "Chinese config switch menu label should mention restart",
);

assert.match(
  zhCN,
  /"agentConfig\.lifecycleTitle": "Agent 安装和更新"/,
  "Chinese lifecycle title should not mention restart",
);

assert.match(
  enUS,
  /"fileTree\.agentInstallUpdate": "Install and update Agent"/,
  "English install/update menu label should not mention restart",
);

assert.match(
  enUS,
  /"fileTree\.switchAgentConfig": "Switch and restart Agent config"/,
  "English config switch menu label should mention restart",
);

assert.match(
  fileTree,
  /onRestartAgent\?: \(agentName: string\) => void \| Promise<void>/,
  "FileTree should expose an agent restart callback",
);

assert.match(
  fileTree,
  /renderEnd=\{\(agent\) => \{[\s\S]*?const restarting = agent\.name === restartingAgent;[\s\S]*?event\.stopPropagation\(\);[\s\S]*?void onRestartAgent\(agent\.name\);[\s\S]*?restarting \? <RestartSpinner \/> : t\("agentConfig\.restart"\)/,
  "Agent config switch agent list should render a spinner while restarting",
);

assert.doesNotMatch(
  fileTree,
  /RestartedAgent|restartedAgent|setAgentConfigRestartedAgent|agentConfigRestartSuccessTimerRef|restarted \? "✓"/,
  "restart success should not render a checkmark or keep success state",
);

assert.doesNotMatch(
  fileTree,
  /const restartAgentFromConfigList[\s\S]*?closeAgentConfigFlow\(\);[\s\S]*?\}, \[closeAgentConfigFlow, onRestartAgent, t\]\);/,
  "restart from the agent list should not close the config switch popover",
);

assert.doesNotMatch(
  fileTree,
  /onRun\(item, "restart"\)/,
  "install/update lifecycle popover should not render a restart action",
);

assert.doesNotMatch(
  fileTree,
  /onClick=\{onRestart\}/,
  "Agent config switch detail view should not render the restart button",
);

// restartAgent 的调用点随「会话域」分层搬进了 useAppSessionLayer（App.tsx 现在只剩四层聚合
// 调用，本身不再 import 任何服务）。契约不变：这个服务必须真的被 import，不能另写一套。
const sessionLayer = readFileSync(new URL("../src/app/useAppSessionLayer.ts", import.meta.url), "utf8");
assert.match(
  sessionLayer,
  /import \{ fetchAgents, restartAgent \} from "\.\.\/services\/agents";/,
  "the session layer must import the existing restartAgent service",
);

assert.match(
  app,
  /onRestartAgent=\{handleRestartAgent\}/,
  "App should pass the restart handler to FileTree",
);
});

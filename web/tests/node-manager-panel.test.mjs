import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const panel = read("src/components/NodeManagerPanel.tsx");
const api = read("src/services/api.ts");
const session = read("src/services/session.ts");
const app = read("src/App.tsx");

// ——— 跨节点抓取：失败必须抛出并给出重试，不能静默吞成 [] ———
// 背景：某个节点偶发拉不到时，原来 catch→[] 会让它整块从项目列表/文件树里凭空消失，
// 且重拉只在 WS 重连或用户操作时发生，没人重试 → 长时间不恢复、也无从知道原因。
assert.match(
  session,
  /console\.error\("\[Session\] Failed to fetch multi-root sessions:", err\);[\s\S]{0,300}?throw err;/,
  "fetchMultiRootSessions 必须把失败抛出，不得吞成 []",
);

assert.match(api, /export async function withNodeRetry<T>\(run: \(\) => Promise<T>\): Promise<T>/, "必须提供 withNodeRetry");
assert.match(api, /err instanceof APIError\s*\?\s*err\.status >= 500\s*:/, "只有 5xx 才重试；4xx 是明确回错，重试无意义");
assert.match(api, /NODE_RETRY_DELAYS_MS/, "重试之间必须有退避间隔");

assert.match(
  app,
  /withNodeRetry\(\(\) => apiProtectedJSON<ManagedRootPayload\[\]>\(appPath\("\/api\/dirs", n\.id\)\)\)/,
  "逐节点 dirs 抓取必须走 withNodeRetry",
);
assert.match(app, /reportError\("node\.load_failed"/, "节点加载失败必须发可见提示");
assert.match(
  app,
  /retryAction: async \(\) => \{[\s\S]{0,240}?await refreshManagedRoots\(\);/,
  "节点加载失败的提示必须带可用的重试动作",
);

// ——— 面板：编辑交互 ———
// 曾经做成「点行内文本即进入编辑」，实测容易误触，改为只认「编辑」按钮。
assert.match(panel, /onClick=\{\(\) => startEdit\(node\)\}/, "「编辑」按钮必须能进入编辑态");
assert.doesNotMatch(
  panel,
  /onClick=\{editing \? undefined : \(\) => startEdit\(node\)\}/,
  "点行内文本不得进入编辑态（易误触），只能点「编辑」按钮",
);

// ——— 面板：local 的特殊处理 ———
assert.match(panel, /disabled=\{isLocal\}/, "local 的地址框必须不可输入");
assert.match(
  panel,
  /isLocal \? \{ name, color: draftColor \} : \{ name, color: draftColor, url: nextUrl \}/,
  "local 的 url 不得提交：读取时会被 deviceLocalNodeURL() 覆盖，提交了也是白提交",
);
assert.match(panel, /\{!isLocal \? \(/, "local 不得有删除入口");

// ——— 面板：改地址的校验 ———
assert.match(
  panel,
  /if \(nextUrl !== node\.url\) \{[\s\S]{0,700}?probeCandidateUrl\(nextUrl\)/,
  "改地址必须先探测连通性：连不上不许保存",
);
assert.match(
  panel,
  /if \(clash\) \{\s*setError\(t\("nodeManager\.urlExists"\)\);/,
  "改地址必须拦截与其它节点同 origin：读取时按 origin 去重会悄悄吃掉一个",
);

// ——— 面板：不得把节点列表镜像进 state ———
// 镜像会变陈旧：改完名面板不刷新，要重载页面才显示。
assert.match(panel, /const nodes = getNodes\(\);/, "面板必须在渲染时直读节点列表");
assert.doesNotMatch(panel, /useState<NodeConnection\[\]>/, "面板不得把节点列表镜像进 state");
assert.match(panel, /addEventListener\("mindfs:nodes-changed"/, "面板必须订阅节点变化并强制重渲染");

console.log("node-manager-panel.test.mjs: OK");

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

// 换模型时 1M / effort 被悄悄重置的回归守卫。
//
// 复现路径：模型下拉的 onSelect 传的是 models[].id，而探测端给的 id **不带 [1m] 后缀**
// （见 AgentSelector 的 submenuSelectedModel 注释）。所以「of[1m] → os」时 nextModel 是裸 "os"，
// 旧代码 has1MSuffix("os") === false → 1M 被判成用户主动关掉 → 勾选框消失且不再补后缀，
// 实际请求退回普通上下文触发压缩。界面上说一套、实际做另一套。
//
// 用真编译器跑 modelUtils（与 dialog-lifecycle.test.mjs 同款做法），不是读源码文本 ——
// 行为变了才会红，改注释/改写法都不会误报。

const root = path.resolve(import.meta.dirname, "..");
const sourcePath = path.join(root, "src/components/action/modelUtils.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const {
  resolveLongContextOnSwitch,
  resolveEffortOnSwitch,
  has1MSuffix,
  strip1MSuffix,
  with1MSuffix,
} = sandbox.exports;

// —— 用户报告的那个场景：of[1m] → os，勾选框明明还亮着 ——
assert.equal(
  resolveLongContextOnSwitch({ nextAgent: "claude", nextModel: "os", prevLongContext: true }),
  true,
  "of[1m] 切到 os 必须保留 1M（下拉回传的是裸 id，不能据此判定用户关了）",
);

// 同族别名互切也要保留
for (const from of ["of", "os", "op", "ok"]) {
  for (const to of ["of", "os", "op", "ok"]) {
    assert.equal(
      resolveLongContextOnSwitch({ nextAgent: "claude", nextModel: to, prevLongContext: true }),
      true,
      `claude ${from}[1m] → ${to} 应保留 1M`,
    );
  }
}

// 本来就没开 1M 的，切完还是没开
assert.equal(
  resolveLongContextOnSwitch({ nextAgent: "claude", nextModel: "os", prevLongContext: false }),
  false,
  "未开启 1M 时不应凭空打开",
);

// 切到非 claude：没有 1M 可谈，返回 false，让上层把勾选框**可见地**关掉
assert.equal(
  resolveLongContextOnSwitch({ nextAgent: "codex", nextModel: "gpt-5", prevLongContext: true }),
  false,
  "切到不支持 1M 的 agent 必须返回 false（可见地关掉，而不是留假勾选）",
);

// 切回 claude 时若之前是关的，仍保持关 —— 不擅自打开
assert.equal(
  resolveLongContextOnSwitch({ nextAgent: "claude", nextModel: "os", prevLongContext: false }),
  false,
  "从 codex 切回 claaude 不应自动打开 1M",
);

// —— effort 继承 ——
assert.equal(
  resolveEffortOnSwitch({
    nextAgent: "claude", nextModel: "os", prevEffort: "high",
    defaultEffort: "medium", availableEfforts: ["low", "medium", "high"],
  }),
  "high",
  "新模型仍支持 high 就该保留用户手选值",
);
assert.equal(
  resolveEffortOnSwitch({
    nextAgent: "claude", nextModel: "os", prevEffort: "xhigh",
    defaultEffort: "medium", availableEfforts: ["low", "medium"],
  }),
  "medium",
  "新模型不支持原值时回落到默认值",
);
assert.equal(
  resolveEffortOnSwitch({
    nextAgent: "codex", nextModel: "gpt-5", prevEffort: "high",
    defaultEffort: "", availableEfforts: [],
  }),
  "",
  "目标不支持 effort 时回落到默认（空）",
);
assert.equal(
  resolveEffortOnSwitch({
    nextAgent: "claude", nextModel: "os", prevEffort: "",
    defaultEffort: "medium", availableEfforts: ["medium"],
  }),
  "medium",
  "原本没选过 effort 时取默认值",
);

// —— 端到端一致性：勾选框状态与实际发送的 model 串必须一致 ——
for (const [prevModel, nextBase] of [["of[1m]", "os"], ["os[1m]", "op"], ["os", "of"]]) {
  const kept = resolveLongContextOnSwitch({ nextAgent: "claude", nextModel: nextBase, prevLongContext: has1MSuffix(prevModel) });
  const sent = with1MSuffix(nextBase, kept);
  assert.equal(
    has1MSuffix(sent),
    kept,
    `切换后勾选(${kept})与实际发送(${sent})不一致：界面上说一套、实际做另一套`,
  );
  assert.equal(strip1MSuffix(sent), nextBase, "基础 model 名不应被继承规则改动");
}

// 非 claude 时：勾选框是关的，发送串里就不能带后缀
const codexKept = resolveLongContextOnSwitch({ nextAgent: "codex", nextModel: "gpt-5", prevLongContext: true });
assert.equal(with1MSuffix("gpt-5", codexKept), "gpt-5", "非 claude 不应发出带 [1m] 的 model");

// —— 调用点接线：修 resolver 还不够，每个 onAgentChange 都得真的用上它 ——
// 三处曾经把 model 写成裸串、effort 写成 ""，于是 resolver 存在却没人调。
// （ActionBar 已在上一轮换用。）
//
// 任务详情面板与任务模板弹窗后来统一到 StageEditor，onAgentChange 随之只剩
// StageEditor 这一处 —— 所以这两个文件只需要经过它，不必自己再调一次 resolver。
const callSites = [
  ["components/StageEditor.tsx", "共享阶段编辑器（详情面板 / 任务模板弹窗共用）"],
  ["components/ScheduledAgentTaskDialog.tsx", "定时任务弹窗（form.model 会原样 POST）"],
];
for (const [rel, label] of callSites) {
  const text = fs.readFileSync(path.join(root, "src", rel), "utf8");
  assert.match(
    text,
    /resolveLongContextOnSwitch\(\{/,
    `${label}(${rel}) 的 onAgentChange 必须用 resolveLongContextOnSwitch 决定 1M`,
  );
  assert.match(
    text,
    /resolveEffortOnSwitch\(\{/,
    `${label}(${rel}) 的 onAgentChange 必须用 resolveEffortOnSwitch 决定 effort`,
  );
  // 不能再出现"model 直接取下拉裸值"的写法
  assert.doesNotMatch(
    text,
    /\bmodel:\s*model\s*\|\|\s*""/,
    `${label}(${rel}): model 不能再直接取裸 id，会丢 [1m]`,
  );
}

// 详情面板 / 模板弹窗不再自带 AgentSelector，必须经 StageEditor 这一个口子走，
// 否则统一之后 resolver 很容易只改到其中一条路径。
for (const [rel, label] of [
  ["components/TaskDetailPanel.tsx", "任务详情面板"],
  ["components/TaskTemplateDialog.tsx", "任务模板弹窗"],
]) {
  const text = fs.readFileSync(path.join(root, "src", rel), "utf8");
  assert.match(text, /<StageEditor\b/, `${label}(${rel}) 必须渲染 StageEditor`);
  assert.doesNotMatch(
    text,
    /<AgentSelector\b/,
    `${label}(${rel}) 不应再直接挂 AgentSelector（改 agent 的语义只在 StageEditor 里一份）`,
  );
}

// 唯一一处「只设 agent+model、不带 1M/effort」的调用点是给会话起名的，
// 那里本来就没有 1M 开关和 effort 概念 —— 钉住这个已知例外，别让人误以为是漏网之鱼。
const fileTree = fs.readFileSync(path.join(root, "src/components/FileTree.tsx"), "utf8");
assert.match(
  fileTree,
  /sessionNamingAgent/,
  "FileTree 的 AgentSelector 是会话起名弹窗（无 1M/effort），保持原样即可",
);

console.log("long-context-inheritance.test.mjs: OK");

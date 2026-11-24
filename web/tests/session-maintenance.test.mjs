import assert from "node:assert/strict";
import { test } from "vitest";
import { rawReadFileSync } from "./source-map.mjs";
import {
  formatMaintenanceBytes,
  groupMaintenanceEntries,
  groupRuntimeIssues,
  maintenanceCapabilities,
  maintenanceCapabilityKeys,
  maintenanceGroupKey,
  maintenanceReclaim,
  runtimeIssueClassLabel,
} from "../src/shared/sessionMaintenance.ts";
import { canRepointSession } from "../src/services/session/sessionMaintenanceApi.ts";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");
const shared = read("../src/shared/sessionMaintenance.ts");
const service = read("../src/services/session/sessionMaintenanceApi.ts");
const hook = read("../src/hooks/useSessionMaintenance.ts");
const sessionPanel = read("../src/components/session/SessionMaintenancePanel.tsx");
const healthPanel = read("../src/components/app/SessionHealthPanel.tsx");
const fileMenu = read("../src/components/file/FileTreeHeaderOverlays.tsx");
const appRoot = read("../src/app/AppRootView.tsx");
const zh = read("../src/i18n/locales/zh-CN-sessions.ts");
const en = read("../src/i18n/locales/en-US-sessions.ts");

const entry = (overrides) => ({
  session_key: "s-1",
  agent: "claude",
  bytes: 1000,
  lines: 10,
  image_bytes: 0,
  runtime_missing: false,
  ...overrides,
});

test("诊断分组：一个问题只落一组，优先级 error > runtime > images > clean", () => {
  assert.equal(maintenanceGroupKey(entry({ error: "broken" })), "attention");
  assert.equal(maintenanceGroupKey(entry({ unsupported: "codex rollout" })), "attention");
  assert.equal(maintenanceGroupKey(entry({ runtime_missing: true, image_bytes: 500 })), "runtime");
  assert.equal(maintenanceGroupKey(entry({ image_bytes: 500 })), "images");
  assert.equal(maintenanceGroupKey(entry({})), "clean");

  const groups = groupMaintenanceEntries({
    scanned: 5,
    sessions: [
      entry({ session_key: "clean" }),
      entry({ session_key: "img", image_bytes: 100 }),
      entry({ session_key: "gone", runtime_missing: true }),
      entry({ session_key: "bad", error: "oops", image_bytes: 100 }),
    ],
  });
  assert.deepEqual(groups.map((group) => group.key), ["attention", "images", "runtime", "clean"]);
  assert.deepEqual(groups[0].entries.map((item) => item.session_key), ["bad"]);
  assert.deepEqual(groupMaintenanceEntries(null), []);
});

test("字节格式与能力文案可独立验证", () => {
  assert.equal(formatMaintenanceBytes(0), "0 B");
  assert.equal(formatMaintenanceBytes(999), "999 B");
  assert.equal(formatMaintenanceBytes(1000), "1.0 KB");
  assert.equal(formatMaintenanceBytes(1_234_000), "1.2 MB");
  assert.equal(formatMaintenanceBytes(-1), "—");

  assert.deepEqual(maintenanceCapabilities(entry({})), { cleanImages: false, purgeEmpty: true });
  assert.deepEqual(maintenanceCapabilities(entry({ image_bytes: 10 })), { cleanImages: true, purgeEmpty: true });
  assert.deepEqual(maintenanceCapabilities(entry({ image_bytes: 10, error: "x" })), {
    cleanImages: false,
    purgeEmpty: false,
  });
  assert.deepEqual(maintenanceCapabilityKeys(entry({ image_bytes: 10 }), true), [
    "sessionMaintenance.cap.diagnose",
    "sessionMaintenance.cap.cleanImages",
    "sessionMaintenance.cap.purgeEmpty",
    "sessionMaintenance.cap.repoint",
  ]);
  assert.ok(!maintenanceCapabilityKeys(entry({}), false).includes("sessionMaintenance.cap.repoint"));
});

test("repoint 能力只判 root 与 running，服务端负责其它 harness 的可读错误", () => {
  assert.equal(canRepointSession({ rootId: "r1" }), true);
  assert.equal(canRepointSession({ rootId: "r1", running: true }), false);
  assert.equal(canRepointSession({ rootId: "" }), false);
  assert.equal(canRepointSession(null), false);
  assert.match(service, /canRepointSession/);
  assert.match(service, /可读错误/);
});

test("回收前后按结果累加，重复结果不能把 before 减成负数", () => {
  const result = maintenanceReclaim(1000, [
    { path: "a", reclaimed_bytes: 300, removed_images: 2 },
    { path: "b", reclaimed_bytes: 200, removed_images: 1 },
  ]);
  assert.deepEqual(result, {
    before: 1000,
    after: 500,
    reclaimed: 500,
    removedImages: 3,
    purged: false,
  });
  assert.equal(maintenanceReclaim(100, [{ path: "x", reclaimed_bytes: 500, purged: true }]).after, 0);
});

test("运行时问题按类别分组，class 文案有兜底且项目顺序稳定", () => {
  const issues = [
    { session_key: "z", agent: "codex", path: "/p", class: "workdir-missing", root_id: "b" },
    { session_key: "a", agent: "claude", path: "/p", class: "worktree-missing", root_id: "a" },
    { session_key: "m", agent: "claude", path: "/p", class: "worktree-missing", root_id: "b" },
  ];
  const groups = groupRuntimeIssues(issues);
  assert.deepEqual(groups.map((group) => group.className), ["workdir-missing", "worktree-missing"]);
  assert.deepEqual(groups[1].rootIds, ["a", "b"]);
  assert.deepEqual(groups[1].issues.map((issue) => issue.session_key), ["a", "m"]);
  assert.equal(runtimeIssueClassLabel("worktree-missing"), "sessionHealth.class.worktree-missing");
  assert.equal(runtimeIssueClassLabel("unknown"), "sessionHealth.class.other");
});

test("HTTP 封装钉住端点与破坏性动作的双保险", () => {
  for (const endpoint of [
    "/api/sessions/maintenance/diagnose",
    "/api/sessions/maintenance/locks/clean",
    "/api/sessions/${encodeURIComponent(sessionKey)}/maintenance/images/clean",
    "/api/sessions/${encodeURIComponent(sessionKey)}/maintenance/purge",
    "/api/sessions/runtime-issues",
    "/api/sessions/runtime-issues/sweep",
  ]) {
    assert.ok(service.includes(endpoint), `service 缺少 ${endpoint}`);
  }
  assert.ok(service.includes("{ confirm: true }"), "purge 必须 confirm:true");
  assert.ok(service.includes("{ apply: true }"), "sweep 必须显式 apply:true");
  assert.ok(service.includes("root_id: rootId"), "写请求必须带 root_id");
  assert.match(service, /encodeURIComponent\(sessionKey\)/);
});

test("三层拆分：视图只经 hook，事件名由 shared 唯一定义", () => {
  for (const panel of [sessionPanel, healthPanel, fileMenu]) {
    assert.ok(!/from ["'][^"']*services\//.test(panel), "view 不许 import services");
  }
  assert.match(sessionPanel, /useSessionMaintenance\(\{ mode: "session" \}\)/);
  assert.match(healthPanel, /useSessionMaintenance\(\{ mode: "health", rootIds \}\)/);
  assert.match(hook, /from ["']\.\.\/services\/session\/sessionMaintenanceApi["']/);
  assert.match(hook, /addEventListener\(\s*SESSION_MAINTENANCE_EVENT/);
  assert.match(hook, /addEventListener\(\s*SESSION_HEALTH_EVENT/);
  assert.match(sessionPanel, /new CustomEvent\(\s*SESSION_MAINTENANCE_EVENT/);
  assert.match(fileMenu, /new CustomEvent\(\s*SESSION_HEALTH_EVENT/);
  assert.match(appRoot, /<SessionHealthPanel rootIds=\{managedRootIds\} \/>/);
  assert.match(appRoot, /<SessionMaintenancePanel \/>/);
});

test("无损边界与确认：没有 commit 按钮、没有剥思考/截负载、purge 两步确认", () => {
  const code = shared + service + hook + sessionPanel + healthPanel;
  // "--drop-tools" 只作为注释里明确禁止的动作出现，不作为可执行参数出现。
  for (const forbidden of ["drop_tools", "strip_thinking", "stripThinking", "truncateTool", "commitSession", "提交修改"]) {
    assert.ok(!code.includes(forbidden), `不该出现 ${forbidden}`);
  }
  assert.match(sessionPanel, /t\("sessionMaintenance\.confirmPurge"\)/);
  assert.match(sessionPanel, /t\("sessionMaintenance\.confirmCleanImages"\)/);
  assert.match(sessionPanel, /t\("sessionMaintenance\.purgeEmpty"\)/);
  assert.match(sessionPanel, /t\("sessionMaintenance\.cleanImages"\)/);
  assert.ok(!/t\("sessionMaintenance\.[^"]*commit/.test(sessionPanel));
});

test("双语键齐备且无漂移", () => {
  const keys = [...zh.matchAll(/"(sessionMaintenance\.[^"]+|sessionHealth\.[^"]+|session\.changeDirNoRoot)":/g)].map((match) => match[1]);
  assert.ok(keys.length >= 40, `维护相关键太少：${keys.length}`);
  for (const key of keys) {
    assert.ok(en.includes(`"${key}":`), `en-US missing ${key}`);
  }
  const enKeys = [...en.matchAll(/"(sessionMaintenance\.[^"]+|sessionHealth\.[^"]+|session\.changeDirNoRoot)":/g)].map((match) => match[1]);
  assert.deepEqual([...keys].sort(), [...enKeys].sort());
});

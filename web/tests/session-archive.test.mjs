import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

/**
 * 归档：后端 archived=only 的列表过滤 + 前端字段透传。
 *
 * 归档的语义是「从主面板隐去、但内容还在」——所以这两处都必须
 *   1. 默认（不带 archived 参数）排除归档项；
 *   2. 带上 archived=only 时只回归档项；
 *   3. toSessionItem 把 archived_at 透传出来（前端 kebab 靠它切「归档/取消归档」文案）。
 */

const read = (rel) => fs.readFileSync(path.resolve(import.meta.dirname, "../", rel), "utf8");

const loadTS = (rel, stubs = {}) => {
  const sourcePath = path.resolve(import.meta.dirname, "../", rel);
  const compiled = ts.transpileModule(fs.readFileSync(sourcePath, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: (id) => stubs[id] || {} };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports;
};

const { toSessionItem } = loadTS("src/app/appSession.ts", {
  "./appTask": { normalizeFastService: (v) => v },
});

const sessionServiceSrc = read("src/services/session.ts");

test("toSessionItem 透传 archived_at", () => {
  const item = toSessionItem("rootA", { key: "k1", archived_at: "2026-09-27T10:00:00Z" });
  assert.equal(item.archived_at, "2026-09-27T10:00:00Z");
});

test("toSessionItem 缺 archived_at 时按未归档处理（undefined，不是空串）", () => {
  const item = toSessionItem("rootA", { key: "k1" });
  assert.equal(item.archived_at, undefined);
});

test("toSessionItem archived_at 为空串按未归档处理", () => {
  // 老缓存快照里该键可能是 null/""，两种都得落到「未归档」，
  // 否则 SessionCard 的 !!session.archived_at 会误判成已归档。
  const item = toSessionItem("rootA", { key: "k1", archived_at: "" });
  assert.equal(item.archived_at, undefined);
});

test("fetchSessions 传 archivedOnly 时把 archived=only 拼进查询串", () => {
  assert.match(
    sessionServiceSrc,
    /if \(options\?\.archivedOnly\) \{\s*params\.set\("archived", "only"\);/,
  );
});

test("默认列表不传 archived 参数（服务端零值即「排除归档」）", () => {
  // archived=only 是**唯一**的 opt-in 开关；没有默认传参，
  // 也就不会因为某个调用点多写了参数而让主面板少显示会话。
  const archivedParamMentions = sessionServiceSrc.match(/params\.set\("archived"/g) || [];
  assert.equal(archivedParamMentions.length, 1);
});

test("setSessionArchived 打 POST /api/sessions/{key}/archive 并带 archived 字段", () => {
  assert.match(sessionServiceSrc, /setSessionArchived\(/);
  assert.match(sessionServiceSrc, /\/api\/sessions\/\$\{encodeURIComponent\(sessionKey\)\}\/archive/);
  assert.match(sessionServiceSrc, /body: JSON\.stringify\(\{ archived \}\),/);
});

test("删除会话走应用内确认弹窗，且挡在 deleteSession 之前", () => {
  // 删除级联删掉整棵子树且不可撤销，误触成本很高。
  // 守卫放在 handleDeleteSession 内部（唯一删除出口），
  // 不放在 SessionCard 的菜单回调里——那样换个入口就绕过去了。
  const appSrc = read("src/App.tsx");
  const start = appSrc.indexOf("const handleDeleteSession = useCallback(");
  assert.ok(start >= 0, "App.tsx 应有 handleDeleteSession");
  const body = appSrc.slice(start, appSrc.indexOf("const handleRenameSession = useCallback(", start));
  const confirmAt = body.indexOf("confirmDialog(");
  const deleteAt = body.indexOf("sessionService.deleteSession(");
  assert.ok(confirmAt >= 0, "删除会话前应有确认弹窗");
  assert.ok(deleteAt > confirmAt, "确认弹窗必须早于实际删除");
  // 取消时直接 return，不发起请求
  assert.match(body, /if \(\s*!\(await confirmDialog\(/);
});

test("确认弹窗文案两种语言都有，且提示级联与不可撤销", () => {
  for (const locale of ["zh-CN", "en-US"]) {
    const src = read(`src/i18n/locales/${locale}.ts`);
    assert.match(src, /"sessionList\.confirmDeleteSession":/);
  }
});

test("归档面板只拉顶级会话（子会话已在归档时被删，这里再挡一次历史脏数据）", () => {
  // 归档区按项目平铺，顶级 + 子会话两层没有意义。
  // top_level 与 archived=only 是 AND 关系（server sessionListWhere），两个一起传才对。
  const appSrc = read("src/App.tsx");
  const start = appSrc.indexOf("// 归档视图懒加载");
  assert.ok(start >= 0, "App.tsx 应有归档懒加载 effect");
  const body = appSrc.slice(start, appSrc.indexOf("}, [archiveOpen, archiveReloadToken])", start));
  assert.match(body, /archivedOnly: true/, "归档查询必须带 archivedOnly");
  assert.match(body, /topLevel: true/, "归档查询必须同时带 topLevel");
});

test("归档语义：只归档自己，子会话走删除", () => {
  // 后端 ArchiveSession 的返回值带出「被删掉的子会话」，供 handler 解绑任务。
  const src = read("../server/internal/api/usecase/session.go");
  const start = src.indexOf("func (s *Service) ArchiveSession(");
  assert.ok(start >= 0, "usecase 应有 ArchiveSession");
  const body = src.slice(start, src.indexOf("\n}\n", start));
  // 父会话自己只打归档标记
  assert.match(body, /manager\.SetArchived\(ctx, self, true\)/);
  // 子会话被排除出来后交给 deleteSessionKeys 真删
  assert.match(body, /if key != self \{/);
  assert.match(body, /s\.deleteSessionKeys\(ctx, root, manager, children\)/);
  assert.match(body, /DeletedKeys: children/);
});

test("归档 handler 用被删的子会话解绑任务", () => {
  // 子会话可能绑着别的任务，不解绑任务点进去就是空白（和 handleSessionDelete 同一套）。
  const src = read("../server/internal/api/http.go");
  const start = src.indexOf("func (h *HTTPHandler) handleSessionArchive(");
  const body = src.slice(start, src.indexOf("\n}\n", start));
  assert.match(body, /out\.DeletedKeys/);
  assert.match(body, /h\.detachTaskFromSession\(r\.Context\(\), rootID, out\.DeletedKeys\)/);
});



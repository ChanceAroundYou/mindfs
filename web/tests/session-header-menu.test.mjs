import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

// 会话标题栏 ⋯ 菜单（修改目录 / 重命名 / 归档 / 删除）的接线契约。
// 钉的是「结构对不对」，真搬运行为在 Go 侧验（session_repoint_test.go）。

test("session-header-menu", async () => {
  const header = readFileSync(new URL("../src/components/session/SessionViewerHeader.tsx", import.meta.url), "utf8");
  const viewer = readFileSync(new URL("../src/components/session/SessionViewer.tsx", import.meta.url), "utf8");
  const menu = readFileSync(new URL("../src/components/session/SessionHeaderMenu.tsx", import.meta.url), "utf8");
  const picker = readFileSync(new URL("../src/components/session/SessionDirectoryPicker.tsx", import.meta.url), "utf8");
  const browser = readFileSync(new URL("../src/components/session/useSessionDirBrowser.ts", import.meta.url), "utf8");
  const sessionView = readFileSync(new URL("../src/app/useAppSessionView.tsx", import.meta.url), "utf8");
  const appSession = readFileSync(new URL("../src/app/appSession.ts", import.meta.url), "utf8");
  const wsMeta = readFileSync(new URL("../src/app/realtimeWsSessionMeta.ts", import.meta.url), "utf8");
  const zh = readFileSync(new URL("../src/i18n/locales/zh-CN.ts", import.meta.url), "utf8");
  const en = readFileSync(new URL("../src/i18n/locales/en-US.ts", import.meta.url), "utf8");

  // ⋯ 在 h1 之后、ViewHeader 之内；h1 flex:1
  assert.match(header, /minWidth: 0,\s*\n\s*flex: 1,\s*\n\s*\}\}\s*\n\s*>/);
  assert.match(header, /\{menu \? <SessionHeaderMenu \{\.\.\.menu\} \/> : null\}\s*\n<\/ViewHeader>/);

  // 只在正式 chat 面板渲染（drawer 不渲染 header）
  assert.match(viewer, /interactionMode === "drawer" \? null : \(\s*\n\s*<SessionViewerHeader[\s\S]{0,400}menu=\{headerMenu\}/);

  // 菜单项接到既有 handler
  for (const handler of ["handleRenameSession", "handleArchiveSession", "handleDeleteSession"]) {
    assert.ok(sessionView.includes(handler + "("));
  }
  assert.ok(sessionView.includes("await promptDialog({"));

  // 换目录走 repoint，且把目标目录一起送过去
  assert.ok(sessionView.includes("sessionService.repointSession("));
  assert.ok(menu.includes("await onRepoint(targetPath)"));

  // 只有 claude 会话能换目录
  assert.ok(menu.includes('String(agent || "") === "claude" && !!rootId'));
  assert.ok(menu.includes("disabled={!canRepoint}"));

  // 路径行：可换行、不存在标红
  assert.ok(menu.includes('color: dirMissing ? "#dc2626" : "var(--text-secondary)"'));
  assert.ok(menu.includes("overflowWrap: \"anywhere\""));
  assert.ok(browser.includes('appURL("/api/local_dirs"'));

  // 选路径复用新建项目控件
  assert.ok(picker.includes('import { LocalPanel } from "../file/ProjectAddPopover";'));
  assert.ok(picker.includes("onLocalNavigate={(path) => void load(path)}"));

  // work_dir 必须穿过三处前端白名单
  assert.ok(appSession.includes("work_dir: typeof session?.work_dir === \"string\" ? session.work_dir : undefined"));
  assert.equal((wsMeta.match(/work_dir:/g) || []).length, 2);

  // 文案双语齐备
  for (const key of ["session.changeDir", "session.dirPicker.title", "session.dirPicker.loadFailed", "session.dirPicker.use", "session.dirPicker.confirm", "session.dirPicker.moving", "session.dirPicker.failed"]) {
    assert.ok(zh.includes("\"" + key + "\""), `zh-CN missing ${key}`);
    assert.ok(en.includes("\"" + key + "\""), `en-US missing ${key}`);
  }
});

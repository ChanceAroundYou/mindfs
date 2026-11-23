import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

/**
 * 2026-11-23 pc 事故：proj 会话里「正常聊天」时凭空多出一条完整副本会话。
 * 取证结论是**误触**——fork 按钮是 18×18 的小图标、与复制键只隔 3px、没有确认、
 * fork 本身又要跑二十多秒（pc 实测 24.4s），期间界面毫无反馈，结束时直接把视图
 * 切到副本上。同一条会话里 b46b808e 已经给「删除会话」加了二次确认，fork 这条
 * 同样的重动作却一直是单击即执行。这组用例把「先确认再 fork」「在途不重发」
 * 两条钉住，删掉任何一条都会红。
 */

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

/** 只取 handleForkAgentMessage 的函数体，避免被同文件的其它调用点蒙混过关。 */
function forkBody(src) {
  const start = src.indexOf("const handleForkAgentMessage");
  assert.ok(start >= 0, "useAppAgentActions.ts 里找不到 handleForkAgentMessage");
  const end = src.indexOf("const handleRunAgentLifecycleCommand", start);
  return src.slice(start, end > start ? end : src.length);
}

test("fork 必须先确认再发请求", () => {
  const src = read("src/app/useAppAgentActions.ts");
  assert.match(src, /import \{ confirmDialog \} from "\.\.\/services\/dialog";/);

  const body = forkBody(src);
  const confirmAt = body.indexOf("confirmDialog(");
  const forkAt = body.indexOf("sessionService.forkSession(");
  assert.ok(confirmAt >= 0, "fork 前必须调 confirmDialog");
  assert.ok(forkAt >= 0, "handleForkAgentMessage 里找不到 forkSession 调用");
  assert.ok(confirmAt < forkAt, "confirmDialog 必须在 forkSession 之前");
  assert.match(body, /t\("session\.forkConfirm"\)/, "确认文案要走 i18n 键");
  assert.match(
    body,
    /if \(!confirmed\) \{\s*return;\s*\}/,
    "用户取消后不得继续 fork",
  );
});

test("fork 在途时不重复发起", () => {
  const src = read("src/app/useAppAgentActions.ts");
  assert.match(src, /useRef/, "在途守卫需要一个 ref");

  const body = forkBody(src);
  assert.match(body, /if \(forkInFlightRef\.current\) \{\s*return;\s*\}/);
  assert.match(body, /forkInFlightRef\.current = true/);
  assert.match(
    body,
    /\} finally \{\s*forkInFlightRef\.current = false;\s*\}/,
    "在途标记必须在 finally 里复位（失败也要能重试）",
  );
});

test("中英双语文案齐全", () => {
  for (const rel of [
    "src/i18n/locales/zh-CN-sessions.ts",
    "src/i18n/locales/en-US-sessions.ts",
  ]) {
    const src = read(rel);
    assert.match(src, /"session\.forkConfirm":/, `${rel} 缺少 session.forkConfirm`);
    assert.match(
      src,
      /"session\.forkConfirmAction":/,
      `${rel} 缺少 session.forkConfirmAction`,
    );
  }
});

import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// RED：切换首帧不得用陈旧快照点亮 pending。
// handleSelectSession 首帧 pending 必须经 resolvePendingForSession 校验真相表，
// 且 stream 订阅重放不得在真相表为 false 时置 isStreaming。

test("switch-no-flash: 首帧 pending 走真相表校验", () => {
  const source = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/app/useSessionSelection.ts"),
    "utf8",
  );
  // 首帧禁止直接用 drawer/session 快照的 pending 做即时渲染。
  assert.doesNotMatch(
    source,
    /const preservePending[\s\S]{0,200}\?\?\s*!!\(currentDrawer/,
    "handleSelectSession must not derive first-frame pending from stale drawer snapshot",
  );
  assert.doesNotMatch(
    source,
    /:[\s]*!!\(session as any\)\?\.pending;/,
    "handleSelectSession must not derive first-frame pending from stale session snapshot",
  );
  // 必须经真相表校验。
  assert.match(
    source,
    /resolvePendingForSession\s*\(\s*targetRoot\s*,\s*key/,
    "handleSelectSession first frame must resolve pending via resolvePendingForSession",
  );
});

test("switch-no-flash: 队列重放不得点亮已结束会话", () => {
  const source = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "src/hooks/useSessionStream.ts"),
    "utf8",
  );
  // onStream 置 isStreaming 前必须有 sessionPending 闸门，否则 dispatcher.pendingStreams
  // 重放会在切换瞬间点亮已结束会话（isAwaiting 那路无闸，照样闪）。
  // 只查 onStream 体（到 onDone 为止）：文件尾 return 处的 `&& sessionPending`
  // 是导出闸门，不算 handler 内闸门。
  const onStreamStart = source.indexOf("onStream: (event)");
  const onStreamBody = source.slice(onStreamStart, source.indexOf("onDone", onStreamStart));
  assert.ok(
    /sessionPending/.test(onStreamBody),
    "useSessionStream onStream must gate replayed stream events on sessionPending",
  );
});

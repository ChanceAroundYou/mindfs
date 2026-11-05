import { test } from "vitest";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import {
  shouldInstallKeyboardPanLock,
  shouldBlockKeyboardPan,
} from "../src/app/nativeShellUtils.ts";

test("shouldInstallKeyboardPanLock: only iOS WebKit or Android shell", () => {
  assert.equal(shouldInstallKeyboardPanLock({ iosWebKit: true, androidRuntime: false }), true);
  assert.equal(shouldInstallKeyboardPanLock({ iosWebKit: false, androidRuntime: true }), true);
  assert.equal(shouldInstallKeyboardPanLock({ iosWebKit: true, androidRuntime: true }), true);
  assert.equal(shouldInstallKeyboardPanLock({ iosWebKit: false, androidRuntime: false }), false);
});

test("shouldBlockKeyboardPan: no editable focus → never", () => {
  assert.equal(
    shouldBlockKeyboardPan({ hasEditableFocus: false, touchCount: 1, targetScrollable: false, targetIsEditable: false }),
    false,
  );
});

test("shouldBlockKeyboardPan: non-single touch → never", () => {
  assert.equal(
    shouldBlockKeyboardPan({ hasEditableFocus: true, touchCount: 2, targetScrollable: false, targetIsEditable: false }),
    false,
  );
});

test("shouldBlockKeyboardPan: scrollable target → allow scroll", () => {
  assert.equal(
    shouldBlockKeyboardPan({ hasEditableFocus: true, touchCount: 1, targetScrollable: true, targetIsEditable: false }),
    false,
  );
});

test("shouldBlockKeyboardPan: touch inside editable → allow caret drag", () => {
  assert.equal(
    shouldBlockKeyboardPan({ hasEditableFocus: true, touchCount: 1, targetScrollable: false, targetIsEditable: true }),
    false,
  );
});

test("shouldBlockKeyboardPan: otherwise block page pan", () => {
  assert.equal(
    shouldBlockKeyboardPan({ hasEditableFocus: true, touchCount: 1, targetScrollable: false, targetIsEditable: false }),
    true,
  );
});

test("source contract: lifecycle installs the lock before native early return", () => {
  const src = readFileSync(new URL("../src/app/useNativeShellLifecycle.ts", import.meta.url), "utf8");
  const installLine = src.indexOf("const uninstallKeyboardPanLock = installKeyboardPanLock();");
  const earlyReturn = src.indexOf("if (!isNativeShellRuntime())");
  assert.ok(installLine >= 0, "install call must exist");
  assert.ok(earlyReturn > installLine, "lock must be installed before the native-shell early return");
  assert.ok(!src.includes("installIOSKeyboardPanLock"), "old name must be gone from lifecycle");
});

test("source contract: message scroller keeps overscroll containment", () => {
  const src = readFileSync(new URL("../src/components/session/SessionViewer.tsx", import.meta.url), "utf8");
  assert.ok(src.includes('overscrollBehaviorY: "contain"'), "scroller must contain overscroll");
});
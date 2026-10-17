import assert from "node:assert/strict";
import { test } from "vitest";
import { installVirtualKeyboardOverlay, keyboardAdjustedViewportHeight } from "../src/app/nativeShellUtils.ts";

// 键盘上浮时视口高度的换算。三种情形必须都对，错一种就是「输入框被顶高一整个键盘」
// 或者「输入框藏在键盘后面」—— 手机上没法自动测，只能把算法钉在这里。
test("keyboardAdjustedViewportHeight", () => {
  // ① 键盘覆盖内容（overlaysContent 生效）：视口没缩，按键盘高度整体抬。
  assert.equal(
    keyboardAdjustedViewportHeight({ rawViewportHeight: 800, largestViewportHeight: 800, imeBottom: 300 }),
    500
  );

  // ② 视口已经被键盘挤小（壳自己缩了 WebView）：缩掉的那 300 不能再扣一次。
  assert.equal(
    keyboardAdjustedViewportHeight({ rawViewportHeight: 500, largestViewportHeight: 800, imeBottom: 300 }),
    500
  );

  // ③ 没有键盘：原样返回。
  assert.equal(
    keyboardAdjustedViewportHeight({ rawViewportHeight: 800, largestViewportHeight: 800, imeBottom: 0 }),
    800
  );

  // ④ 键盘比视口还高（横屏小窗）：兜底 320，不返回负数。
  assert.equal(
    keyboardAdjustedViewportHeight({ rawViewportHeight: 300, largestViewportHeight: 300, imeBottom: 500 }),
    320
  );
});

// 装上覆盖模式：没有 VirtualKeyboard API（桌面 / iOS）时必须是个安全的空操作，
// 有的话要开 overlaysContent 并盯住 geometrychange。
test("installVirtualKeyboardOverlay", () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const withKeyboard = (virtualKeyboard) => {
    Object.defineProperty(globalThis, "navigator", {
      value: { virtualKeyboard },
      configurable: true,
    });
  };

  try {
    withKeyboard(undefined);
    assert.equal(typeof installVirtualKeyboardOverlay(), "function");

    const events = [];
    const fake = {
      overlaysContent: false,
      addEventListener: (type) => events.push(type),
      removeEventListener: (type) => events.push(`off:${type}`),
    };
    withKeyboard(fake);
    const uninstall = installVirtualKeyboardOverlay();
    assert.equal(fake.overlaysContent, true);
    uninstall();
    assert.deepEqual(events, ["geometrychange", "off:geometrychange"]);
  } finally {
    if (original) {
      Object.defineProperty(globalThis, "navigator", original);
    }
  }
});

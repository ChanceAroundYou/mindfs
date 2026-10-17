import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("keyboard-no-white-flash (A+B)", async () => {
  const root = path.resolve(import.meta.dirname, "..");
  const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

  // B: WebView 不再随键盘 resize，差额落在 WebView 内，由 CSS 底色兜底。
  const manifest = read("../android/app/src/main/AndroidManifest.xml");
  assert.match(manifest, /android:windowSoftInputMode="adjustNothing"/);

  // A: 窗口/WebView 背景跟随应用主题。
  const styles = read("../android/app/src/main/res/values/styles.xml");
  assert.match(styles, /name="android:windowBackground">@color\/mindfsWindowBackground/);
  const colors = read("../android/app/src/main/res/values/colors.xml");
  const colorsNight = read("../android/app/src/main/res/values-night/colors.xml");
  assert.match(colors, /name="mindfsWindowBackground"/);
  assert.match(colorsNight, /name="mindfsWindowBackground"/);

  const css = read("src/index.css");
  assert.match(css, /html,\s*body,\s*#root\s*\{[^}]*background-color:\s*var\(--bg-gradient-start/);

  const main = read("../android/app/src/main/java/com/mindfs/app/MainActivity.java");
  assert.match(main, /MindFSThemeBackground/);
  assert.match(main, /setBackgroundColor/);

  const appearance = read("src/services/prefs/appearance.ts");
  assert.match(appearance, /export function syncNativeWindowBackground/);
  assert.match(appearance, /MindFSThemeBackground/);

  const html = read("index.html");
  assert.match(html, /MindFSThemeBackground\.setBackgroundColor/);
});

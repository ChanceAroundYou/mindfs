import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("keyboard-no-white-flash", async () => {
  const root = path.resolve(import.meta.dirname, "..");
  const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

  // 关键一条：布局视口不能跟着键盘缩。缩了 → 画布跟着缩 → 键盘滑上来之前那块空隙
  // 由宿主窗口底色填充（手机上实测是 #F8FAFC，即 manifest 的 background_color）。
  // 显式声明 resizes-visual，别依赖浏览器默认值。
  const html = read("index.html");
  assert.match(html, /interactive-widget=resizes-visual/);
  assert.doesNotMatch(html, /interactive-widget=resizes-content/);

  // Android 壳仍按 adjustResize 走：syncViewportHeight 的 resizedBy 分支就是为它写的。
  const manifest = read("../android/app/src/main/AndroidManifest.xml");
  assert.match(manifest, /android:windowSoftInputMode="adjustResize"/);

  // 壳自己缩了 WebView 时，露出的那条缝是窗口底色 —— 必须跟着应用主题，
  // 否则暗色主题下闪白。颜色 + 桥两端都要在。
  const styles = read("../android/app/src/main/res/values/styles.xml");
  assert.match(styles, /name="android:windowBackground">@color\/mindfsWindowBackground/);
  assert.match(read("../android/app/src/main/res/values/colors.xml"), /name="mindfsWindowBackground"/);
  assert.match(read("../android/app/src/main/res/values-night/colors.xml"), /name="mindfsWindowBackground"/);

  const main = read("../android/app/src/main/java/com/mindfs/app/MainActivity.java");
  assert.match(main, /MindFSThemeBackground/);

  const appearance = read("src/services/prefs/appearance.ts");
  assert.match(appearance, /export function syncNativeWindowBackground/);
  assert.match(appearance, /MindFSThemeBackground/);
  assert.match(html, /MindFSThemeBackground\.setBackgroundColor/);

  // PWA（Edge/Chrome 装到主屏）另有第三条缝：壳自己的窗口底色，取自 manifest 的
  // background_color，安装时定死 —— 运行时改不了（meta theme-color 管不到它，
  // 实测暗色主题下仍闪 #F8FAFC）。所以按主题换 manifest：暗色装暗色壳。
  const lightManifest = JSON.parse(read("public/manifest.webmanifest"));
  const darkManifest = JSON.parse(read("public/manifest-dark.webmanifest"));
  assert.equal(darkManifest.background_color, "#0f172a");
  assert.equal(lightManifest.start_url, darkManifest.start_url);
  assert.equal(lightManifest.scope, darkManifest.scope);
  assert.notEqual(lightManifest.background_color, darkManifest.background_color);
  assert.match(html, /link\[rel="manifest"\]/);
  assert.match(html, /manifest-dark\.webmanifest/);
});

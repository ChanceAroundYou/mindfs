// 版本身份在本项目里只有一种语义：semver 核心 + 构建号（发布 tag 形如 v0.5.5-1115）。
//
// 三处必须同一条公式，改一边就得改另一边：
//   · web/android/app/build.gradle 的 mindfsVersionCodeFromName → APK 的 android:versionCode
//   · server/internal/update/version.go 的 parseVersionParts    → 后端更新检查
//   · 本文件                                                     → 原生壳的更新提示
//
// 曾经的坏法是把版本截断到第一个非数字/点，于是 v0.5.5-1115 与 v0.5.5-1116 都变成
// [0,5,5]：装着 1115 的节点和手机永远显示「已是最新」，更新按钮点了也不动。
// git describe 的装饰（-<commits>-g<sha>[-dirty]）必须先整块剥掉，否则提交数会被读成版本段。

const describeDecoration = /-\d+-g[0-9a-fA-F]+$/;

export function normalizeVersionLabel(value: string): string {
  return String(value || "").trim().replace(/^v/i, "");
}

export function versionParts(value: string): number[] | null {
  const text = normalizeVersionLabel(value).replace(/-dirty$/, "").replace(describeDecoration, "");
  if (!text) {
    return null;
  }
  const parts: number[] = [];
  for (const segment of text.split(/[.-]/)) {
    if (!segment) {
      continue;
    }
    // 非数字段（rc1、裸哈希、dev）不是可比较的版本段：停在这里，
    // 但已经解析出来的前导段仍然有效 —— 不把整个版本判为无效。
    if (!/^\d+$/.test(segment)) {
      break;
    }
    parts.push(Number.parseInt(segment, 10));
  }
  return parts.length ? parts : null;
}

export function isNewerVersion(latest: string, current: string): boolean {
  const latestParts = versionParts(latest);
  const currentParts = versionParts(current);
  if (!latestParts || !currentParts) {
    return normalizeVersionLabel(latest) !== normalizeVersionLabel(current);
  }
  const length = Math.max(latestParts.length, currentParts.length);
  for (let i = 0; i < length; i += 1) {
    const left = latestParts[i] || 0;
    const right = currentParts[i] || 0;
    if (left > right) return true;
    if (left < right) return false;
  }
  return false;
}

// 与 gradle 同一条公式（android:versionCode 就是它算出来的）：Android 的安装器只认这个整数，
// 「同 semver 但构建号更大」必须由它区分。iOS 侧 CFBundleVersion 也按同一个数写。
//
// 分段宽度：build 占 10^4、patch 占 10^6、minor 占 10^8 ⇒ 可表示范围 major<21、minor<100、
// patch<100、build<10000，最大约 2.1e9，正好落在 Android versionCode 的 Int32 里。
// 旧公式是 `major*1e7 + minor*1e5 + patch*1e3 + build`，而我们的构建号已经四位（v0.5.5-1115）——
// build 溢出进 patch 的槽位：0.5.5-1115 算出 506116，0.5.6 只有 506001，
// 于是 Android 认为 0.5.6【更旧】、拒绝安装。改成分段宽度后 0.5.5-1115 = 5051116、
// 0.5.6 = 5060001，序关系恢复。新号码也比旧号码整体更大，装着旧公式包的机器能直接升上来。
export function nativeBuildCodeFromTag(tag: string): number {
  const parts = versionParts(tag);
  if (!parts || parts.length < 3) {
    return 0;
  }
  const [major, minor, patch, build = 0] = parts;
  // 越界不算数：算出来的序关系是错的，比「没有版本身份」更坏。
  // gradle 侧同样越界直接 fail，发布构建不会带着错号出厂。
  if (major >= 21 || minor >= 100 || patch >= 100 || build >= 10000) {
    return 0;
  }
  return Math.max(1, major * 100000000 + minor * 1000000 + patch * 10000 + build + 1);
}

// APK 自更新走「发布清单」而不是未认证 GitHub REST API 的行为契约。
//
// 背景（2026-11-05 实测）：旧实现 fetch api.github.com/repos/.../releases。
// 未认证限流 60 次/小时且【按出口 IP 计】，手机在内网走机场出口，同一落地机
// IP 上挂着大量他人流量，配额常年 0/60：
//     走机场出口 403 "API rate limit exceeded for 52.199.72.174"
//     自家出口   200，core 57/60
// GitHub 节点是 fallback 组、16 成员轮换 → 时而选中被用光的 IP 就 403，
// 表现为「检查更新时好时坏」。
//
// 这里钉住：主路径用 releases/latest/download 固定 URL 读清单、不碰 REST API；
// 解析对非 APK 产物（.deb / 别家平台的包）/ 多 download / 缺字段都要判断正确；版本判定与 gradle 一致。
import assert from "node:assert/strict";
import { test } from "vitest";
import {
  isNewerVersion,
  nativeBuildCodeFromTag,
  versionParts,
} from "../src/services/update/appVersion.ts";
import { releaseFromManifest } from "../src/services/update/githubAppUpdate.ts";

const manifest = {
  version: "v0.5.5-1114",
  notes: "# MindFS v0.5.5-1114\n\n## 新功能\n- 提示音分类",
  downloads: [
    {
      os: "android",
      arch: "universal",
      filename: "mindfs_v0.5.5-1114_android.apk",
      url: "https://github.com/ChanceAroundYou/mindfs/releases/download/v0.5.5-1114/mindfs_v0.5.5-1114_android.apk",
      size: 7612473,
    },
  ],
};

test("manifest parses into an installable release", () => {
  const release = releaseFromManifest(manifest);
  assert.ok(release, "manifest should yield a release");
  assert.equal(release.tag, "v0.5.5-1114");
  assert.equal(release.filename, "mindfs_v0.5.5-1114_android.apk");
  assert.match(release.notes, /^# MindFS v0\.5\.5-1114/);
  // 关键：下载地址是发布资源，不是 REST API。
  assert.doesNotMatch(release.downloadUrl, /api\.github\.com/);
  assert.match(release.downloadUrl, /releases\/download\/v0\.5\.5-1114\/.+\.apk$/);
});

test("versionCode formula stays in lockstep with gradle", () => {
  // gradle: Math.max(1, major*100000000 + minor*1000000 + patch*10000 + build + 1)
  assert.equal(nativeBuildCodeFromTag("v0.5.5-1114"), 5 * 1000000 + 5 * 10000 + 1114 + 1);
  assert.equal(nativeBuildCodeFromTag("v0.5.5"), 5 * 1000000 + 5 * 10000 + 1);
  assert.equal(nativeBuildCodeFromTag("0.5.5"), 5050001);
  // gradle 容忍 tag 之后的构建串，前端取同一前缀即可，不能因此落回 1。
  assert.equal(nativeBuildCodeFromTag("v0.5.5-1108-2-gabc1234"), 5 * 1000000 + 5 * 10000 + 1108 + 1);
  assert.equal(nativeBuildCodeFromTag("nightly"), 0);
  assert.equal(nativeBuildCodeFromTag(""), 0);
});

// 构建号四位（v0.5.5-1115）必须待在 patch 槽之下。旧公式给 build 只留 10^3：
// 0.5.5-1115 → 506116，0.5.6 → 506001，安装器会把 0.5.6 判成更旧的包拒绝安装。
test("a semver bump outranks the largest build number", () => {
  assert.ok(nativeBuildCodeFromTag("v0.5.6") > nativeBuildCodeFromTag("v0.5.5-1115"));
  assert.ok(nativeBuildCodeFromTag("v0.5.5-1115") > nativeBuildCodeFromTag("v0.5.5-1114"));
  assert.ok(nativeBuildCodeFromTag("v0.6.0") > nativeBuildCodeFromTag("v0.5.9-9999"));
  // 整数身份必须在 Int32 内（Android versionCode 是 int）：分段宽度的可表示上限。
  assert.ok(nativeBuildCodeFromTag("v0.5.5-1115") < 2 ** 31);
  assert.ok(nativeBuildCodeFromTag("v20.99.99-9999") < 2 ** 31);
  // 越界不当版本用（算出来的序是错的）：TS 返回 0 = 不是可用身份，gradle 侧直接 fail 构建。
  assert.equal(nativeBuildCodeFromTag("v21.0.0"), 0);
  assert.equal(nativeBuildCodeFromTag("v0.100.0"), 0);
  assert.equal(nativeBuildCodeFromTag("v0.5.100"), 0);
  assert.equal(nativeBuildCodeFromTag("v0.5.5-10000"), 0);
});

// 壳里的两条通道（登录页提示 appUpdate.ts / 自动更新 githubAppUpdate.ts）必须给同一个答案。
test("shell update notice and APK channel agree on the same pair", () => {
  const pairs: Array<[string, string, boolean]> = [
    ["v0.5.5-1116", "v0.5.5-1115", true],
    ["v0.5.5-1115", "v0.5.5-1115-1-gabc1234", false],
    ["v0.5.5-1115", "v0.5.5-1116", false],
    ["v0.5.6", "v0.5.5-1115", true],
    ["v0.5.5", "v0.5.5-1115", false],
  ];
  for (const [latest, current, want] of pairs) {
    assert.equal(isNewerVersion(latest, current), want, `isNewerVersion(${latest}, ${current})`);
    assert.equal(
      nativeBuildCodeFromTag(latest) > nativeBuildCodeFromTag(current),
      want,
      `versionCode 比对与 isNewerVersion 对 ${latest} vs ${current} 给出不同答案`,
    );
  }
  // 截断式解析（旧实现）会把这两个 tag 都读成 [0,5,5]，构建号更新当场不可见。
  assert.notDeepEqual(versionParts("v0.5.5-1116"), versionParts("v0.5.5-1115"));
});

test("manifest without a usable APK yields null (never a bogus update)", () => {
  assert.equal(releaseFromManifest({ version: "v0.5.5-1114" }), null);
  assert.equal(releaseFromManifest({ downloads: manifest.downloads }), null);
  // 非 APK 产物不能当成 APK 推给 Android：不给 os 字段，只靠扩展名这一层判别。
  assert.equal(
    releaseFromManifest({
      version: "v0.5.5-1114",
      downloads: [{ filename: "mindfs-linux.deb", url: "https://x/y.deb" }],
    }),
    null,
  );
});

test("multiple downloads: the android apk wins over other platforms", () => {
  const release = releaseFromManifest({
    version: "v0.5.5-1114",
    downloads: [
      { os: "ios", filename: "mindfs-ios.ipa", url: "https://x/a.ipa" },
      { os: "android", filename: "mindfs_v0.5.5-1114_android.apk", url: "https://x/b.apk" },
    ],
  });
  assert.equal(release?.filename, "mindfs_v0.5.5-1114_android.apk");
});

test("update decision: equal or older manifest means already latest", () => {
  const decide = (current, latest) =>
    nativeBuildCodeFromTag(latest) <= current ? "latest" : "available";
  assert.equal(decide(nativeBuildCodeFromTag("v0.5.5-1114"), "v0.5.5-1114"), "latest");
  assert.equal(decide(nativeBuildCodeFromTag("v0.5.5-1113"), "v0.5.5-1114"), "available");
  // 装的是比清单更新的构建（tag 之后自己打的包）→ 不该提示更新。
  assert.equal(decide(nativeBuildCodeFromTag("v0.5.5-1115"), "v0.5.5-1114"), "latest");
});
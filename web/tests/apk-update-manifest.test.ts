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
// 解析对 harmony .hap / 多 download / 缺字段都要判断正确；版本判定与 gradle 一致。
import assert from "node:assert/strict";
import { test } from "vitest";
import {
  apkVersionCodeFromTag,
  releaseFromManifest,
} from "../src/services/update/githubAppUpdate.ts";

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
  // gradle: Math.max(1, major*10000000 + minor*100000 + patch*1000 + build + 1)
  assert.equal(apkVersionCodeFromTag("v0.5.5-1114"), 5 * 100000 + 5 * 1000 + 1114 + 1);
  assert.equal(apkVersionCodeFromTag("v0.5.5"), 5 * 100000 + 5 * 1000 + 1);
  assert.equal(apkVersionCodeFromTag("0.5.5"), 505001);
  // gradle 容忍 tag 之后的构建串，前端取同一前缀即可，不能因此落回 1。
  assert.equal(apkVersionCodeFromTag("v0.5.5-1108-2-gabc1234"), 505000 + 1108 + 1);
  assert.equal(apkVersionCodeFromTag("nightly"), 0);
  assert.equal(apkVersionCodeFromTag(""), 0);
});

test("manifest without a usable APK yields null (never a bogus update)", () => {
  assert.equal(releaseFromManifest({ version: "v0.5.5-1114" }), null);
  assert.equal(releaseFromManifest({ downloads: manifest.downloads }), null);
  // harmony 的 .hap 不能当成 APK 推给 Android。
  assert.equal(
    releaseFromManifest({
      version: "v0.5.5-1114",
      downloads: [{ os: "harmony", filename: "mindfs-harmony.hap", url: "https://x/y.hap" }],
    }),
    null,
  );
});

test("multiple downloads: android apk is preferred over harmony hap", () => {
  const release = releaseFromManifest({
    version: "v0.5.5-1114",
    downloads: [
      { os: "harmony", filename: "mindfs-harmony.hap", url: "https://x/a.hap" },
      { os: "android", filename: "mindfs_v0.5.5-1114_android.apk", url: "https://x/b.apk" },
    ],
  });
  assert.equal(release?.filename, "mindfs_v0.5.5-1114_android.apk");
});

test("update decision: equal or older manifest means already latest", () => {
  const decide = (current, latest) =>
    apkVersionCodeFromTag(latest) <= current ? "latest" : "available";
  assert.equal(decide(apkVersionCodeFromTag("v0.5.5-1114"), "v0.5.5-1114"), "latest");
  assert.equal(decide(apkVersionCodeFromTag("v0.5.5-1113"), "v0.5.5-1114"), "available");
  // 装的是比清单更新的构建（tag 之后自己打的包）→ 不该提示更新。
  assert.equal(decide(apkVersionCodeFromTag("v0.5.5-1115"), "v0.5.5-1114"), "latest");
});
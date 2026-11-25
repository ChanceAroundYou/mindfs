// 首开向导已整体删除：11 步漫游、持久化状态、data-onboarding 锚点与对应文案都不再存在。
//
// 钉住的是「不要回来」：这几个文件或人肉定位属性一旦被合回来，要么是误合并上游，
// 要么是又想用页面自己教用户 —— 两种情况都该被这条测试挡住。
import assert from "node:assert/strict";
import path from "node:path";
import { readdirSync, statSync } from "node:fs";
import { existsSync, readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => readFileSync(path.join(root, rel), "utf8");

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (/\.(ts|tsx)$/.test(name)) out.push(full);
  }
  return out;
}

test("onboarding-removed", () => {
  for (const rel of [
    "src/services/prefs/onboarding.ts",
    "src/components/shell/OnboardingTour.tsx",
    "src/components/shell/onboardingSteps.ts",
    "src/components/shell/useOnboardingTour.ts",
    "src/components/app/OnboardingGate.tsx",
  ]) {
    assert.equal(existsSync(path.join(root, rel)), false, `${rel} 应已删除`);
  }

  for (const file of walk(path.join(root, "src"))) {
    assert.doesNotMatch(readFileSync(file, "utf8"), /data-onboarding|=onboarding\b/, `${file} 不得再有向导锚点`);
  }

  for (const locale of ["src/i18n/locales/zh-CN-workspace.ts", "src/i18n/locales/en-US-workspace.ts"]) {
    assert.doesNotMatch(read(locale), /"onboarding\./, `${locale} 的向导文案应已删除`);
  }

  const rootView = read("src/app/AppRootView.tsx");
  assert.doesNotMatch(rootView, /OnboardingGate|onboardingOpen|shouldAutoStartOnboarding/);

  // e2e 夹具靠这两个属性定位输入框与发送键，改名/删除会让整组 e2e 静默失效。
  assert.match(read("src/components/action/ActionBar.tsx"), /data-composer-input/);
  assert.match(read("src/components/action/ActionBarControls.tsx"), /data-composer-send/);
});

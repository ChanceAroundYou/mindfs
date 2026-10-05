// 切换会话时消息列表闪动（2026-10-05 实测复现）。
//
// 症状：点开另一个会话，屏幕先显示**上一个会话**的消息，随后整块塌成空白，
// 约 0.8s 后新会话的消息才补回来。用户的描述是「完整对话 → 塌成最后一条用户消息 → 慢慢补回来」。
//
// 根因：SessionViewer 的窗口化视图把 visibleExchanges 放在组件 state 里，而两处
// <SessionViewer> 都没给 key —— 切会话时组件不重挂载，visibleExchanges 跨会话复用：
//   1. 切换那一帧先拿上一个会话的窗口渲染（屏幕上属于 A，内容是 B）；
//   2. init effect（依赖 [sessionKey, rootId, applyWindow]）重跑，用**尚未就绪**的
//      session.exchanges 当种子 —— 新会话此时还没进缓存，toSessionItem 又不复制
//      exchanges 字段，种子为空 → setVisibleExchanges([]) 把画面清空；
//   3. getSessionWindow 回来后 applyWindow 才重新填上（keepOlder 合并的是刚被清空的 prev）。
//
// 这些断言钉的是「两处调用点都必须给 key」——修复本身只有两行，但一旦有人删掉 key，
// 症状会退化成很难在 review 里看出来的闪烁，所以要有回归护栏。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";

const appPath = path.resolve(import.meta.dirname, "../src/App.tsx");
const appSrc = fs.readFileSync(appPath, "utf8");
const appCode = appSrc.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, "");

const tests = [];

// 两处 <SessionViewer> 调用点：主区（sessionView）与浮层（drawerSessionSnapshot）。
// 用「紧邻的 session={...} 属性名」区分，因为 JSX 里 key 写在 session 之前。
function viewerOpenTags() {
  const out = [];
  for (const m of appSrc.matchAll(/<SessionViewer\b([\s\S]*?)\/>/g)) {
    out.push({ props: m[1], index: m.index });
  }
  return out;
}

const tags = viewerOpenTags();

tests.push([
  "两处 <SessionViewer> 都存在（主区 + 会话浮层）",
  () => {
    assert.equal(tags.length, 2, `期望 2 处 <SessionViewer>，实际 ${tags.length} 处`);
  },
]);

tests.push([
  "每个 <SessionViewer> 都带 key，否则窗口化可见集会跨会话复用",
  () => {
    for (const [i, tag] of tags.entries()) {
      assert.match(
        tag.props,
        /\bkey\s*=/,
        `第 ${i + 1} 处 <SessionViewer> 缺 key prop —— ` +
          "切会话不会重挂载，visibleExchanges 沿用上一个会话的数据，" +
          "表现为「显示上一个会话 → 塌成空白 → 新内容慢慢补回来」。",
      );
    }
  },
]);

tests.push([
  "key 用会话键，不是常量（常量等于没给）",
  () => {
    for (const [i, tag] of tags.entries()) {
      const m = tag.props.match(/\bkey\s*=\s*\{([^}]*)\}/);
      assert.ok(m, `第 ${i + 1} 处 <SessionViewer> 的 key 不是表达式`);
      assert.match(
        m[1],
        /\.key\b|key\b/,
        `第 ${i + 1} 处 <SessionViewer> 的 key 表达式里没有会话键：${m[1]}`,
      );
      // 常量字符串 key 不会随会话变，等于没给。
      assert.doesNotMatch(
        tag.props,
        /\bkey\s*=\s*"(?!no-)[^"]*"/,
        `第 ${i + 1} 处 <SessionViewer> 的 key 是常量字符串，切换时不会变`,
      );
    }
  },
]);

tests.push([
  "key 绑定的会话键与该处的 session prop 是同一个",
  () => {
    // 主区：key={selectedSessionSnapshot?.key} session={selectedSessionSnapshot}
    // 浮层：key={drawerSessionSnapshot.key}    session={drawerSessionSnapshot}
    for (const [i, tag] of tags.entries()) {
      const keyExpr = (tag.props.match(/\bkey\s*=\s*\{([^}]*)\}/) || [])[1] || "";
      const sessExpr = (tag.props.match(/\bsession\s*=\s*\{([^}]*)\}/) || [])[1] || "";
      assert.ok(sessExpr, `第 ${i + 1} 处 <SessionViewer> 读不出 session prop`);
      // key={snapshot?.key || "fallback"} —— 表达式里带 || 兜底，比较前先砍掉。
      const keyRoot = keyExpr
        .split("||")[0]
        .replace(/\?\.key\b|\.key\b/, "")
        .trim();
      assert.equal(
        keyRoot,
        sessExpr.trim(),
        `第 ${i + 1} 处 key 与 session 指向不同对象：key=${keyRoot} session=${sessExpr}`,
      );
    }
  },
]);

// 反向护栏：SessionViewer 的可见集确实是组件 state，所以「不重挂载 = 跨会话复用」。
// 若将来有人把 visibleExchanges 提到全局 store，本护栏就该失效并重新评估 key 的必要性。
tests.push([
  "前提仍然成立：可见集是组件 state（不是全局 store）",
  () => {
    assert.match(
      appCode,
      /<SessionViewer/,
      "App.tsx 里找不到 <SessionViewer> —— 护栏需要跟着更新",
    );
    const viewerSrc = fs.readFileSync(
      path.resolve(import.meta.dirname, "../src/components/SessionViewer.tsx"),
      "utf8",
    );
    assert.match(
      viewerSrc,
      /useState<ExchangeArray>\(/,
      "SessionViewer 里找不到 visibleExchanges 的 useState —— " +
        "若可见集已提到全局 store，key 的必要性需重新评估",
    );
  },
]);

for (const [name, fn] of tests) {
  test(name, fn);
}
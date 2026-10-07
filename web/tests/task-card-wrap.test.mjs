import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 任务卡排版在窄容器下的换行契约。
//
// 背景：卡片标题原来是一条 nowrap 的 flex 行，挤了编号/名字/阶段/状态/worktree 五项，
// 只有名字可伸缩。flex 先按 base size 排完一行、再收缩，于是名字被挤到只剩 23px ≈ 2 个汉字
// （220px 工作台卡；移动端项目看板两列只有 161px，更糟）。
// 看板与工作台共用 TaskCardRows，所以这一处守卫同时覆盖两个面。
//
// 排版细节本身是**渲染出来的**，测试测不出宽度；这里守的是「不退回 nowrap 硬截」这条
// 结构不变量。真机验证见计划里的浏览器实测清单。

const cardRows = readFileSync(new URL("../src/components/TaskCardRows.tsx", import.meta.url), "utf8");
const styles = readFileSync(new URL("../src/components/workspace/workspaceStyles.ts", import.meta.url), "utf8");

// 1) 标题行必须允许换行 —— 否则窄容器里五项仍然挤一条线，名字继续被压扁
assert.match(cardRows, /flexWrap: "wrap"/, "标题行应允许换行（窄容器里五项不再挤同一条线）");

// 2) 任务名是 2 行截断，不是单行省略号。
//    必须带 -webkit-box + WebkitLineClamp + WebkitBoxOrient 三件套才是真截断，
//    少任何一件都会退化成「不限行数的普通块」或「根本不截断」。
//    刻意不锚定属性顺序：纯重排（formatter、字母序）不该让测试红。
const clampProps = cardRows.match(
  /style=\{\{[\s\S]*?display: "-webkit-box",[\s\S]*?WebkitLineClamp: 2,[\s\S]*?WebkitBoxOrient: "vertical",[\s\S]*?\}\}/,
);
assert.ok(clampProps, "任务名应是 2 行截断（-webkit-box + WebkitLineClamp + WebkitBoxOrient 三件套齐全）");

// 3) flex-basis 必须在：换行按 base size 判定、收缩只发生在换行之后。
//    没有明确 basis 时名字的 auto(=max-content) 会被当成占满整行，加 flexWrap 也不换。
assert.match(
  cardRows,
  /flex: "1 1 50%",\s*\n\s*minWidth: 0,/,
  "任务名要有明确的 flex basis（这是换行能否生效的关键）",
);

// 4) 长英文名/URL 也要能断，否则 break-word 不够，卡片会横向溢出撑破网格
assert.match(
  cardRows,
  /wordBreak: "break-word",\s*\n\s*overflowWrap: "anywhere"/,
  "任务名要允许在任意位置断行（长英文/URL 不该撑破卡片）",
);

// 5) 「· + 内容」必须合成一个 flex item。
//    裸的「·」自己是独立 item，换行时会留在上一行末尾、把它后面的阶段/状态推到下一行，
//    排出来是行首一个孤零零的「· worktree」（实测 161px 卡必现）。旧的 nowrap 挤不出这形态，
//    是开了 flexWrap 才引入的 —— 所以这条与 flexWrap 成对出现，缺一不可。
assert.match(
  cardRows,
  /const taskMetaPairStyle[\s\S]*?flex: "0 1 auto"/,
  "「· + 内容」应合成一个 flex item（taskMetaPairStyle）",
);
const separatorCount = (cardRows.match(/opacity: 0\.55/g) || []).length;
const pairedCount = (cardRows.match(/style=\{taskMetaPairStyle\}/g) || []).length;
assert.ok(
  pairedCount > 0 && separatorCount <= pairedCount + 1,
  `每个「·」都应包在配对容器里（分隔符 ${separatorCount} 个，配对 ${pairedCount} 处）`,
);

// 5) 操作行同样允许换行：窄卡上左侧会话 chip 与右侧按钮组也可能放不下
assert.match(cardRows, /justifyContent: "space-between", gap: "4px", flexWrap: "wrap"/, "操作行应允许换行");
assert.match(
  cardRows,
  /justifyContent: "flex-end", gap: 0, marginLeft: "auto"/,
  "按钮组靠 marginLeft:auto 换行后仍贴右",
);

// 6) 名字是卡片上信息量最高的字段，必须有 title 兜底（2 行仍装不下的超长名）
assert.match(cardRows, /title=\{taskName\}/, "任务名应带 title 兜底");

// 10) 字号必须留在行内 style —— 这条不是洁癖。
//     index.css 的分区字号缩放靠 [style*="font-size: Npx"] 属性选择器匹配行内字面量，
//     搬进 CSS 类会让整个 main 区域字号缩放静默失效（无报错、难归因）。
//     本次刻意不引入任何新 CSS 类、不动 index.css —— 两个文件都受这条约束（后者同样带行内字号）。
assert.doesNotMatch(
  cardRows,
  /className=/,
  "卡片布局不得搬到 CSS 类里：字号缩放依赖行内 [style*=\"font-size: Npx\"] 选择器",
);
assert.doesNotMatch(
  styles,
  /className=/,
  "工作台样式同样不得搬到 CSS 类里（它也带行内 fontSize，受同一条约束）",
);

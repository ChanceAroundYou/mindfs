// related-files 刷新的去抖（2026-10-07）。
//
// 症状（实测）：20 分钟内同一会话被拉了 74 次 `related-files`，呈每秒 4–6 个的
// 突发。根因是后端 shared_watcher 每写一个文件就发一条
// `session.related_files.updated`，前端 handler 每次原样拉一次（无去抖），
// 而 agent 连写文件时这些回包高度重复（载荷 3.5–26 KB）。
//
// 契约：同一 (rootID, sessionKey) 的多次触发在 500ms 窗口内合并成一次请求。
//
// 为什么用源码断言而不是行为测试：去抖逻辑嵌在 useRealtimeEvents 的 useMemo
// 闭包里，且依赖 window.setTimeout 与一个 ref；把它拆出来只为测试会打散这段
// 「订阅器只派发」的结构（见该文件顶部注释）。这里改为钉住三个必须同时成立的
// 形状，任一被上游合并冲掉都会红。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const src = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/app/useRealtimeEvents.ts"),
  "utf8",
);

// ① 去抖表必须存在，且以 ref 持有（不是每次渲染新建的 Map）
assert.match(
  src,
  /const relatedFilesRefreshTimers = useRef\(new Map<string, number>\(\)\);/,
  "去抖表必须以 ref 持有，否则每次渲染都会丢在途定时器",
);

// ② 键必须含 rootID 与 sessionKey：同一会话合并、不同会话不互相顶掉
assert.match(
  src,
  /const debounceKey = `\$\{rootID\}::\$\{sessionKey\}`;/,
  "去抖键必须按 rootID::sessionKey 分组",
);

// ③ 触发时必须先清掉上一个定时器，再挂新的 —— 这就是「合并」本身
assert.match(
  src,
  /const existing = relatedFilesRefreshTimers\.current\.get\(debounceKey\);\s*\n\s*if \(existing\) \{\s*\n\s*window\.clearTimeout\(existing\);\s*\n\s*\}/,
  "重复触发必须清掉上一个定时器（否则不是合并，是排队发多次）",
);

// ④ 真正的请求必须在定时器回调里发，而不是在触发时直接发
assert.match(
  src,
  /window\.setTimeout\(\(\) => \{[\s\S]*?sessionService\.getSessionRelatedFiles\([\s\S]*?\}, 500\);/,
  "getSessionRelatedFiles 必须延到 500ms 定时器里发",
);

// ⑤ 触发方必须是「派发」而非 await：原实现是 async，改成 fire-and-forget
assert.doesNotMatch(
  src,
  /const refreshSessionRelatedFiles = async \(/,
  "refreshSessionRelatedFiles 不应再是 async（去抖后没有可 await 的即时结果）",
);

console.log("related-files-debounce: ok");

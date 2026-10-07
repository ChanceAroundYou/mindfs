// 测试预加载：全局注册 ts-module-hook，让**静态** import 也能解析到移动后的物理文件。
//
// 为什么需要它：`diagram-zoom.test.mjs` 等用的是静态
// `import { x } from "../src/components/diagramZoom.ts"`，而静态导入在测试体执行前
// 就完成解析 —— 测试里再 `register()` 已经晚了。`--import` 预加载发生在任何测试文件
// 之前，在这里注册 resolver 才能覆盖静态导入。
//
// 与 source-map-hook.mjs 的分工：那个 patch `fs.readFileSync`（读源码文本），
// 这个管模块解析（`import` 语句）。两者都只对「命中 source-map 映射」的路径生效。
import { register } from "node:module";

register("./ts-module-hook.mjs", import.meta.url);

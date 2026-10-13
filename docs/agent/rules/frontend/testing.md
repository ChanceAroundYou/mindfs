# 测试规范（长期规则）

- 现状：前端 `web/tests/` 当前有 Vitest `*.test.ts` 与存量 120 个 Node `*.test.mjs`；`npm test`
  先跑 Vitest，再跑带 source-map hook 的 Node 兼容链路。存量 Node 文件按触及时逐步迁移。
  后端 `server/` 下 `*_test.go`，`make test`。
- **新复用件必须带测试**：新进 `shared/` 的函数、新通用组件，同批加 `web/tests/*.test.ts`（Vitest）；
  存量补测见 `TODO.md`。
- **前端测试栈切 Vitest**：新测试统一使用 Vitest；Node 旧测试暂由独立兼容脚本运行，按触及节奏逐步迁入 Vitest。
- **命名规范测试**：新建 `web/tests/naming-convention.test.mjs`，扫描 `src/` 校验
  文件名 = 主 export 名、`.ts` 无 JSX、Utils/Helpers 后缀语义。

## 测试命令

```bash
cd web && npm test        # Vitest 新测试 + 带源码映射 hook 的 Node 兼容测试
make test                 # 后端：Go 测试
```
